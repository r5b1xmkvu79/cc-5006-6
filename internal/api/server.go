// Package api exposes the editing station over HTTP: project CRUD with
// reopen-safe media references, media import with probing, validation,
// and export. Geometry and validation rules live in the project/render
// packages; this layer only orchestrates.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"oralcut/internal/media"
	"oralcut/internal/project"
	"oralcut/internal/render"
)

// Server wires the HTTP API.
type Server struct {
	Prober    *media.Prober
	FFmpeg    string
	ProjectsDir string
	DeliverDir  string
	// MediaRoot confines importable/source files to a known tree.
	MediaRoot string
	TempDir   string
}

func New(ffprobe, ffmpeg, projectsDir, deliverDir, mediaRoot, tempDir string) *Server {
	return &Server{
		Prober: media.NewProber(ffprobe), FFmpeg: ffmpeg,
		ProjectsDir: projectsDir, DeliverDir: deliverDir,
		MediaRoot: mediaRoot, TempDir: tempDir,
	}
}

// Routes returns the API handler (no static files; see cmd/server).
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/media/scan", s.scanMedia)
	mux.HandleFunc("POST /api/projects", s.createProject)
	mux.HandleFunc("GET /api/projects/{id}", s.getProject)
	mux.HandleFunc("PUT /api/projects/{id}", s.saveProject)
	mux.HandleFunc("POST /api/projects/{id}/media/import", s.importMedia)
	mux.HandleFunc("GET /api/projects/{id}/media/{mid}/file", s.mediaFile)
	mux.HandleFunc("GET /api/projects/{id}/media/{mid}/thumbnail", s.thumbnail)
	mux.HandleFunc("POST /api/projects/{id}/validate", s.validate)
	mux.HandleFunc("POST /api/projects/{id}/export", s.export)
	mux.HandleFunc("GET /api/projects/{id}/export/manifest", s.exportManifest)
	return s.log(mux)
}

func (s *Server) log(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

// scanMedia lists importable files under the media root.
func (s *Server) scanMedia(w http.ResponseWriter, r *http.Request) {
	exts := map[string]bool{".mp4": true, ".mov": true, ".mkv": true,
		".jpg": true, ".jpeg": true, ".png": true, ".tif": true, ".tiff": true}
	var files []string
	filepath.Walk(s.MediaRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if exts[strings.ToLower(filepath.Ext(path))] {
			files = append(files, path)
		}
		return nil
	})
	writeJSON(w, 200, map[string]any{"mediaRoot": s.MediaRoot, "files": files})
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid JSON")
		return
	}
	if req.ID == "" || strings.ContainsAny(req.ID, "/\\..") {
		writeErr(w, 400, "invalid project id")
		return
	}
	p := project.New(req.ID, req.Title)
	if err := p.Save(s.ProjectsDir); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, p)
}

// getProject reloads the project and re-verifies every media reference,
// so reopening always reflects the current filesystem.
func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	p, err := project.Load(s.ProjectsDir, r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, "project not found")
		return
	}
	p.RecheckMedia(s.hash)
	writeJSON(w, 200, p)
}

func (s *Server) saveProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := project.Load(s.ProjectsDir, id); err != nil {
		writeErr(w, 404, "project not found")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		writeErr(w, 400, "read body failed")
		return
	}
	var p project.Project
	if err := json.Unmarshal(body, &p); err != nil {
		writeErr(w, 400, "invalid project JSON")
		return
	}
	if p.ID != id {
		writeErr(w, 400, "project id mismatch")
		return
	}
	p.RecheckMedia(s.hash)
	if err := p.Save(s.ProjectsDir); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, &p)
}

// importMedia probes a file under MediaRoot and registers it. Only
// references are stored; originals stay in place.
func (s *Server) importMedia(w http.ResponseWriter, r *http.Request) {
	p, err := project.Load(s.ProjectsDir, r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, "project not found")
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid JSON")
		return
	}
	abs, err := filepath.Abs(req.Path)
	if err != nil || !strings.HasPrefix(abs, s.MediaRoot+string(os.PathSeparator)) {
		writeErr(w, 403, "path outside media root")
		return
	}
	info, err := s.Prober.Probe(abs)
	if err != nil {
		writeErr(w, 422, err.Error())
		return
	}
	m := project.Media{
		ID: fmt.Sprintf("m%d", len(p.Media)+1), Path: abs, SHA256: info.SHA256,
		Kind: string(info.Kind), Width: info.Width, Height: info.Height,
		Rotation: info.Rotation, EXIFOrientation: info.EXIFOrientation,
		Duration: info.Duration, HasAudio: info.HasAudio, Title: info.Title,
		Status: project.StatusOK,
	}
	p.Media = append(p.Media, m)
	if err := p.Save(s.ProjectsDir); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, m)
}

// mediaFile streams the original source for preview. Thumbnails are
// generated separately and never used as a final-frame source.
func (s *Server) mediaFile(w http.ResponseWriter, r *http.Request) {
	m, ok := s.findMedia(w, r)
	if !ok {
		return
	}
	if m.Status != project.StatusOK {
		writeErr(w, 409, "media unresolved: "+m.Reason)
		return
	}
	http.ServeFile(w, r, m.Path)
}

// thumbnail generates (and caches) a small preview image.
func (s *Server) thumbnail(w http.ResponseWriter, r *http.Request) {
	m, ok := s.findMedia(w, r)
	if !ok {
		return
	}
	if m.Status != project.StatusOK {
		writeErr(w, 409, "media unresolved: "+m.Reason)
		return
	}
	thumb := filepath.Join(s.TempDir, "thumbs", m.ID+".jpg")
	if _, err := os.Stat(thumb); err != nil {
		if err := os.MkdirAll(filepath.Dir(thumb), 0o755); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		if err := s.makeThumbnail(m, thumb); err != nil {
			writeErr(w, 500, "thumbnail failed: "+err.Error())
			return
		}
	}
	http.ServeFile(w, r, thumb)
}

func (s *Server) validate(w http.ResponseWriter, r *http.Request) {
	p, err := project.Load(s.ProjectsDir, r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, "project not found")
		return
	}
	var req struct {
		AllowPlaceholder bool `json:"allowPlaceholder"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	p.RecheckMedia(s.hash)
	issues := p.Validate(req.AllowPlaceholder)
	writeJSON(w, 200, map[string]any{
		"issues":  issues,
		"blocked": project.HasErrors(issues),
	})
}

// export runs the render. Unresolved clips block unless the caller
// explicitly allows placeholders; either way the manifest explains.
func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	p, err := project.Load(s.ProjectsDir, r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, "project not found")
		return
	}
	var req struct {
		AllowPlaceholder bool `json:"allowPlaceholder"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	p.RecheckMedia(s.hash)

	dir := filepath.Join(s.DeliverDir, p.ID)
	exp := &render.Exporter{FFmpeg: s.FFmpeg, TempDir: filepath.Join(s.TempDir, "export_"+p.ID)}
	manifest, err := exp.Export(p, render.Options{
		OutPath:          filepath.Join(dir, "program.mp4"),
		AllowPlaceholder: req.AllowPlaceholder,
	})
	if err != nil {
		var ve *render.ValidationError
		if errors.As(err, &ve) {
			writeJSON(w, 422, map[string]any{"error": err.Error(), "issues": ve.Issues})
			return
		}
		writeErr(w, 500, err.Error())
		return
	}
	if err := render.WriteManifest(filepath.Join(dir, "manifest.json"), manifest); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, manifest)
}

func (s *Server) exportManifest(w http.ResponseWriter, r *http.Request) {
	path := filepath.Join(s.DeliverDir, r.PathValue("id"), "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		writeErr(w, 404, "no export yet")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write(data)
}

func (s *Server) findMedia(w http.ResponseWriter, r *http.Request) (*project.Media, bool) {
	p, err := project.Load(s.ProjectsDir, r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, "project not found")
		return nil, false
	}
	p.RecheckMedia(s.hash)
	m := p.MediaByID(r.PathValue("mid"))
	if m == nil {
		writeErr(w, 404, "media not found")
		return nil, false
	}
	return m, true
}

func (s *Server) hash(path string) (string, error) {
	info, err := s.Prober.Probe(path)
	if err != nil {
		return "", err
	}
	return info.SHA256, nil
}

func (s *Server) makeThumbnail(m *project.Media, out string) error {
	// 320px on the long side, rotation normalized, single frame.
	args := []string{"-v", "error"}
	if m.Kind == "image" {
		args = append(args, "-loop", "1", "-t", "0.1")
	}
	args = append(args, "-i", m.Path,
		"-vf", "scale='if(gt(iw,ih),320,-2)':'if(gt(iw,ih),-2,320)'",
		"-frames:v", "1", "-q:v", "5", "-y", out)
	return runCmd(s.FFmpeg, args...)
}

func runCmd(bin string, args ...string) error {
	cmd := exec.Command(bin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w\n%s", bin, err, out)
	}
	return nil
}
