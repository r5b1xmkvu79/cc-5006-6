// Package render turns a validated project into an ffmpeg filter graph
// and runs the export. All composition math comes from internal/geometry,
// the same rules the preview uses.
package render

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"oralcut/internal/geometry"
	"oralcut/internal/project"
)

// Exporter renders projects with the ffmpeg toolchain.
type Exporter struct {
	FFmpeg  string
	TempDir string // placeholder slates and intermediate files
}

// Options control one export run.
type Options struct {
	OutPath         string
	AllowPlaceholder bool
}

// ClipManifest records how one clip was rendered.
type ClipManifest struct {
	ClipID       string  `json:"clipId"`
	Label        string  `json:"label"`
	MediaID      string  `json:"mediaId"`
	SourcePath   string  `json:"sourcePath,omitempty"`
	SourceSHA256 string  `json:"sourceSha256,omitempty"`
	Fit          string  `json:"fit"`
	DisplayClaim string  `json:"displayClaim"` // "complete" | "cropped"
	Crop         *project.Crop `json:"crop,omitempty"`
	In           float64 `json:"in,omitempty"`
	Out          float64 `json:"out,omitempty"`
	Dwell        float64 `json:"dwell,omitempty"`
	Duration     float64 `json:"duration"`
	Placeholder  bool    `json:"placeholder"`
	Reason       string  `json:"reason,omitempty"`
}

// Manifest is the export record delivered alongside the media file.
type Manifest struct {
	ProjectID   string          `json:"projectId"`
	ProjectTitle string         `json:"projectTitle"`
	Canvas      project.Canvas  `json:"canvas"`
	ExportedAt  string          `json:"exportedAt"`
	Output      string          `json:"output"`
	Clips       []ClipManifest  `json:"clips"`
	Warnings    []string        `json:"warnings,omitempty"`
}

// Export validates the project, builds the ffmpeg graph, and renders.
func (e *Exporter) Export(p *project.Project, opts Options) (*Manifest, error) {
	issues := p.Validate(opts.AllowPlaceholder)
	if project.HasErrors(issues) {
		return nil, &ValidationError{Issues: issues}
	}
	if len(p.Clips) == 0 {
		return nil, fmt.Errorf("project has no clips")
	}
	if err := os.MkdirAll(e.TempDir, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(opts.OutPath), 0o755); err != nil {
		return nil, err
	}

	g := &graph{}
	var args []string
	manifest := &Manifest{
		ProjectID: p.ID, ProjectTitle: p.Title, Canvas: p.Canvas,
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Output: opts.OutPath,
	}
	for _, i := range issues {
		manifest.Warnings = append(manifest.Warnings, i.Message)
	}

	cw, ch := p.Canvas.Width, p.Canvas.Height
	for idx, clip := range p.Clips {
		m := p.MediaByID(clip.MediaID)
		cm := ClipManifest{
			ClipID: clip.ID, Label: clip.Label, MediaID: clip.MediaID,
			Fit: string(clip.Fit), DisplayClaim: clip.DisplayClaim(),
			Duration: clip.Duration(),
		}
		if clip.Fit == geometry.FitCrop {
			c := clip.Crop
			cm.Crop = &c
		}
		if clip.Kind == project.ClipVideo {
			cm.In, cm.Out = clip.In, clip.Out
		} else {
			cm.Dwell = clip.Dwell
		}

		var vlabel, alabel string
		if m == nil || m.Status != project.StatusOK {
			// Unresolved: render a placeholder slate, timing preserved.
			reason := "media reference missing"
			if m != nil {
				reason = m.Reason
			}
			cm.Placeholder = true
			cm.Reason = reason
			png := filepath.Join(e.TempDir, fmt.Sprintf("placeholder_%d.png", idx))
			if err := PlaceholderPNG(png, clip.MediaID, reason, cw, ch); err != nil {
				return nil, err
			}
			dur := clip.Duration()
			args = append(args, "-loop", "1", "-framerate", fmt.Sprint(p.Canvas.FPS),
				"-t", fmt.Sprintf("%.6f", dur), "-i", png)
			inIdx := len(g.inputs)
			g.inputs = append(g.inputs, png)
			vlabel = g.chain(fmt.Sprintf("[%d:v]setpts=PTS-STARTPTS,fps=%d,format=yuv420p,setsar=1,settb=AVTB",
				inIdx, p.Canvas.FPS))
			alabel = g.silence(dur)
		} else {
			cm.SourcePath = m.Path
			cm.SourceSHA256 = m.SHA256
			var err error
			vlabel, alabel, args, err = e.buildClip(g, args, p, &clip, m, idx)
			if err != nil {
				return nil, fmt.Errorf("clip %s: %w", clip.ID, err)
			}
		}
		g.concat = append(g.concat, vlabel, alabel)
		manifest.Clips = append(manifest.Clips, cm)
	}

	n := len(p.Clips)
	var sb strings.Builder
	for i, part := range g.parts {
		if i > 0 {
			sb.WriteString(";\n")
		}
		sb.WriteString(part)
	}
	sb.WriteString(";\n")
	for _, l := range g.concat {
		sb.WriteString("[" + l + "]")
	}
	fmt.Fprintf(&sb, "concat=n=%d:v=1:a=1[vout][aout]", n)

	args = append(args,
		"-filter_complex", sb.String(),
		"-map", "[vout]", "-map", "[aout]",
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "18",
		"-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "192k",
		"-movflags", "+faststart",
		"-y", opts.OutPath)

	cmd := exec.Command(e.FFmpeg, args...)
	var log strings.Builder
	cmd.Stderr = &log
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg export failed: %w\n%s", err, tail(log.String(), 4000))
	}
	return manifest, nil
}

// ValidationError carries blocking issues back to the caller.
type ValidationError struct {
	Issues []project.Issue
}

func (e *ValidationError) Error() string {
	var b strings.Builder
	b.WriteString("export blocked by unresolved issues:")
	for _, i := range e.Issues {
		fmt.Fprintf(&b, "\n- [%s] %s", i.Level, i.Message)
	}
	return b.String()
}

// graph accumulates filter chains and concat labels.
type graph struct {
	parts   []string
	concat  []string
	inputs  []string
	counter int
}

func (g *graph) label(prefix string) string {
	l := fmt.Sprintf("%s%d", prefix, g.counter)
	g.counter++
	return l
}

// chain appends a filter chain whose last output is unlabeled; it adds an
// output label and returns it.
func (g *graph) chain(body string) string {
	out := g.label("v")
	g.parts = append(g.parts, body+"["+out+"]")
	return out
}

// silence adds a silent stereo 48kHz source of the given duration.
func (g *graph) silence(dur float64) string {
	out := g.label("a")
	g.parts = append(g.parts, fmt.Sprintf(
		"anullsrc=channel_layout=stereo:sample_rate=48000,atrim=duration=%.6f,asetpts=PTS-STARTPTS[%s]",
		dur, out))
	return out
}

// buildClip wires inputs and filter chains for one resolved clip.
func (e *Exporter) buildClip(g *graph, args []string, p *project.Project,
	clip *project.Clip, m *project.Media, idx int) (vlabel, alabel string, outArgs []string, err error) {

	cw, ch := p.Canvas.Width, p.Canvas.Height
	dur := clip.Duration()

	// Input. Rotation metadata is normalized explicitly below, so ffmpeg
	// auto-rotation is disabled to avoid double transforms.
	if clip.Kind == project.ClipPhoto {
		args = append(args, "-loop", "1", "-framerate", fmt.Sprint(p.Canvas.FPS),
			"-t", fmt.Sprintf("%.6f", dur), "-noautorotate", "-i", m.Path)
	} else {
		args = append(args, "-noautorotate", "-i", m.Path)
	}
	inIdx := len(g.inputs)
	g.inputs = append(g.inputs, m.Path)

	// 1) Normalize rotation first.
	var pre strings.Builder
	fmt.Fprintf(&pre, "[%d:v]", inIdx)
	if clip.Kind == project.ClipVideo {
		fmt.Fprintf(&pre, "trim=start=%.6f:end=%.6f,setpts=PTS-STARTPTS,", clip.In, clip.Out)
	} else {
		pre.WriteString("setpts=PTS-STARTPTS,")
	}
	pre.WriteString(rotationFilters(m))

	// 2) Composition on the normalized frame.
	pre.WriteString(fitFilters(p, clip, m, cw, ch, idx))

	fmt.Fprintf(&pre, ",fps=%d,format=yuv420p,setsar=1,settb=AVTB", p.Canvas.FPS)
	vlabel = g.chain(pre.String())

	// 3) Audio: real track trimmed in sync, or generated silence.
	if clip.Kind == project.ClipVideo && m.HasAudio {
		out := g.label("a")
		g.parts = append(g.parts, fmt.Sprintf(
			"[%d:a]atrim=start=%.6f:end=%.6f,asetpts=PTS-STARTPTS,aresample=48000,"+
				"aformat=sample_fmts=fltp:channel_layouts=stereo[%s]",
			inIdx, clip.In, clip.Out, out))
		alabel = out
	} else {
		alabel = g.silence(dur)
	}
	return vlabel, alabel, args, nil
}

// rotationFilters returns the normalization filters for container
// rotation or EXIF orientation. Applied before any composition.
func rotationFilters(m *project.Media) string {
	if m.Kind == "image" {
		switch exifOrientationOf(m) {
		case 2:
			return "hflip,"
		case 3:
			return "hflip,vflip,"
		case 4:
			return "vflip,"
		case 5:
			return "transpose=0,"
		case 6:
			return "transpose=1,"
		case 7:
			return "transpose=3,"
		case 8:
			return "transpose=2,"
		}
		return ""
	}
	// Container display matrix. Verified against ffmpeg autorotate:
	// rotation=90 displays as stored rotated 90° counterclockwise.
	switch ((m.Rotation%360)+360)%360 {
	case 90:
		return "transpose=2,"
	case 180:
		return "hflip,vflip,"
	case 270:
		return "transpose=1,"
	}
	return ""
}

// exifOrientationOf reads the raw EXIF tag kept on the media record.
func exifOrientationOf(m *project.Media) int {
	return m.EXIFOrientation
}

// fitFilters builds the composition chain for one clip. idx uniquifies
// filter labels inside the shared graph.
func fitFilters(p *project.Project, clip *project.Clip, m *project.Media, cw, ch, idx int) string {
	sw, sh := float64(m.Width), float64(m.Height)
	cwf, chf := float64(cw), float64(ch)

	switch clip.Fit {
	case geometry.FitFull:
		l, _ := geometry.LayoutFor(sw, sh, cwf, chf, clip.Fit, 0, 0)
		fw, fh := geometry.Even(l.Fg.W), geometry.Even(l.Fg.H)
		fx := clampInt(int(l.Fg.X+0.5), 0, cw-fw)
		fy := clampInt(int(l.Fg.Y+0.5), 0, ch-fh)
		return fmt.Sprintf("scale=%d:%d:flags=lanczos,pad=%d:%d:%d:%d:color=%s",
			fw, fh, cw, ch, fx, fy, p.Canvas.Background)

	case geometry.FitCrop:
		if clip.Pan != nil {
			return panFilters(p, clip, m, cw, ch)
		}
		l, _ := geometry.LayoutFor(sw, sh, cwf, chf, clip.Fit, clip.Crop.X, clip.Crop.Y)
		scaledW := geometry.Even(sw * l.Scale)
		scaledH := geometry.Even(sh * l.Scale)
		vx := clampInt(int(l.SrcVisible.X*l.Scale+0.5), 0, scaledW-cw)
		vy := clampInt(int(l.SrcVisible.Y*l.Scale+0.5), 0, scaledH-ch)
		return fmt.Sprintf("scale=%d:%d:flags=lanczos,crop=%d:%d:%d:%d",
			scaledW, scaledH, cw, ch, vx, vy)

	case geometry.FitBorder:
		blur := clip.Border.Blur
		if blur <= 0 {
			blur = 20
		}
		pad := clip.Border.Padding
		color := clip.Border.Color
		if color == "" {
			color = "black"
		}
		availW, availH := cwf-2*float64(pad), chf-2*float64(pad)
		s := fitScaleF(availW/sw, availH/sh)
		fw, fh := geometry.Even(sw*s), geometry.Even(sh*s)
		fx := clampInt((cw-fw)/2, 0, cw-fw)
		fy := clampInt((ch-fh)/2, 0, ch-fh)
		// split -> blurred cover background + fitted foreground overlay.
		// Labels carry the clip index to stay unique in the graph.
		u := fmt.Sprintf("c%d", idx)
		var b strings.Builder
		b.WriteString(fmt.Sprintf("split=2[bgs%[1]s][fgs%[1]s];"+
			"[bgs%[1]s]scale=%d:%d:force_original_aspect_ratio=increase:flags=lanczos,"+
			"crop=%d:%d,boxblur=%d:2[bgb%[1]s];"+
			"[fgs%[1]s]scale=%d:%d:flags=lanczos[fgb%[1]s];",
			u, cw, ch, cw, ch, blur, fw, fh))
		if pad > 0 {
			b.WriteString(fmt.Sprintf("[bgb%[1]s]drawbox=x=%d:y=%d:w=%d:h=%d:color=%s:t=fill[bgp%[1]s];",
				u, fx-pad, fy-pad, fw+2*pad, fh+2*pad, color))
			b.WriteString(fmt.Sprintf("[bgp%[1]s][fgb%[1]s]overlay=%d:%d", u, fx, fy))
		} else {
			b.WriteString(fmt.Sprintf("[bgb%[1]s][fgb%[1]s]overlay=%d:%d", u, fx, fy))
		}
		return b.String()
	}
	return ""
}

// panFilters renders a slow photo pan: scale to cover*zoom, then a crop
// viewport moving linearly over the dwell. The viewport is validated to
// stay inside the scaled image for the whole range.
func panFilters(p *project.Project, clip *project.Clip, m *project.Media, cw, ch int) string {
	sw, sh := float64(m.Width), float64(m.Height)
	pan := *clip.Pan
	// Resolve endpoints against the rounded scaled size actually used.
	base := coverScaleF(sw, sh, float64(cw), float64(ch))
	scale := base * pan.Zoom
	scaledW := geometry.Even(sw * scale)
	scaledH := geometry.Even(sh * scale)
	overX := scaledW - cw
	overY := scaledH - ch
	clamp01 := func(v float64) float64 {
		if v < 0 {
			return 0
		}
		if v > 1 {
			return 1
		}
		return v
	}
	x0 := int(clamp01(pan.X0)*float64(overX) + 0.5)
	y0 := int(clamp01(pan.Y0)*float64(overY) + 0.5)
	x1 := int(clamp01(pan.X1)*float64(overX) + 0.5)
	y1 := int(clamp01(pan.Y1)*float64(overY) + 0.5)
	d := clip.Dwell
	return fmt.Sprintf(
		"scale=%d:%d:flags=lanczos,"+
			"crop=%d:%d:x='%d%+d*t/%.6f':y='%d%+d*t/%.6f'",
		scaledW, scaledH, cw, ch,
		x0, x1-x0, d, y0, y1-y0, d)
}

func fitScaleF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func coverScaleF(sw, sh, cw, ch float64) float64 {
	a, b := cw/sw, ch/sh
	if a > b {
		return a
	}
	return b
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// WriteManifest persists the export manifest next to the output.
func WriteManifest(path string, m *Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
