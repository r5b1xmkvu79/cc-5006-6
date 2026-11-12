package render

import (
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"oralcut/internal/geometry"
	"oralcut/internal/media"
	"oralcut/internal/project"
)

func toolchain(t *testing.T) (ffmpeg, ffprobe string) {
	t.Helper()
	ffmpeg = os.Getenv("ORALCUT_FFMPEG")
	ffprobe = os.Getenv("ORALCUT_FFPROBE")
	if ffmpeg == "" || ffprobe == "" {
		t.Skip("ORALCUT_FFMPEG/ORALCUT_FFPROBE not set")
	}
	return ffmpeg, ffprobe
}

func assetsDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("ORALCUT_ASSETS")
	if dir == "" {
		dir = "../../assets"
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(abs, "interview_16x9.mp4")); err != nil {
		t.Skipf("assets not generated: %v", err)
	}
	return abs
}

// probeMedia builds a project.Media from a probed file.
func probeMedia(t *testing.T, ffprobe, id, path string) project.Media {
	t.Helper()
	info, err := media.NewProber(ffprobe).Probe(path)
	if err != nil {
		t.Fatalf("probe %s: %v", path, err)
	}
	return project.Media{
		ID: id, Path: path, SHA256: info.SHA256,
		Kind: string(info.Kind), Width: info.Width, Height: info.Height,
		Rotation: info.Rotation, EXIFOrientation: info.EXIFOrientation,
		Duration: info.Duration, HasAudio: info.HasAudio, Title: info.Title,
		Status: project.StatusOK,
	}
}

// buildDemoProject assembles the full archive mix: landscape interview,
// narrow old tape, stills, vertical manuscript, rotated phone video,
// EXIF portrait, silent video, panning photo, and one missing source.
func buildDemoProject(t *testing.T, ffprobe, assets string) *project.Project {
	t.Helper()
	p := project.New("demo", "口述纪录片档案混编测试")
	a := func(name string) string { return filepath.Join(assets, name) }

	p.Media = []project.Media{
		probeMedia(t, ffprobe, "interview", a("interview_16x9.mp4")),
		probeMedia(t, ffprobe, "oldtape", a("oldtape_4x3.mp4")),
		probeMedia(t, ffprobe, "photo", a("photo_landscape.jpg")),
		probeMedia(t, ffprobe, "manuscript", a("manuscript_vertical.jpg")),
		probeMedia(t, ffprobe, "phone", a("phone_rotated.mp4")),
		probeMedia(t, ffprobe, "exifportrait", a("photo_portrait_exif6.jpg")),
		probeMedia(t, ffprobe, "silent", a("noaudio_16x9.mp4")),
		probeMedia(t, ffprobe, "narrow", a("extreme_narrow.jpg")),
		{
			ID: "lost", Path: a("lost_reel_1998.mp4"), Kind: "video",
			Width: 1920, Height: 1080, Duration: 10,
		},
	}
	confirmed := project.Crop{X: 0.5, Y: 0, Confirmed: true, ConfirmedBy: "editor"}
	p.Clips = []project.Clip{
		{ID: "c1", MediaID: "interview", Kind: project.ClipVideo, Label: "横屏访谈",
			In: 1, Out: 4, Fit: geometry.FitFull},
		{ID: "c2", MediaID: "oldtape", Kind: project.ClipVideo, Label: "窄幅旧录像",
			In: 0, Out: 2, Fit: geometry.FitCrop, Crop: confirmed},
		{ID: "c3", MediaID: "photo", Kind: project.ClipPhoto, Label: "静态照片",
			Dwell: 2, Fit: geometry.FitFull},
		{ID: "c4", MediaID: "manuscript", Kind: project.ClipPhoto, Label: "竖向手稿",
			Dwell: 2, Fit: geometry.FitBorder, Border: project.Border{Blur: 12, Padding: 10, Color: "red"}},
		{ID: "c5", MediaID: "phone", Kind: project.ClipVideo, Label: "旋转手机视频",
			In: 0, Out: 2, Fit: geometry.FitFull},
		{ID: "c6", MediaID: "exifportrait", Kind: project.ClipPhoto, Label: "EXIF肖像照",
			Dwell: 2, Fit: geometry.FitFull},
		{ID: "c7", MediaID: "silent", Kind: project.ClipVideo, Label: "无声视频",
			In: 0, Out: 2, Fit: geometry.FitFull},
		{ID: "c8", MediaID: "photo", Kind: project.ClipPhoto, Label: "照片缓慢平移",
			Dwell: 3, Fit: geometry.FitCrop,
			Crop: project.Crop{X: 0.5, Y: 0.5, Confirmed: true, ConfirmedBy: "editor"},
			Pan: &geometry.Pan{Zoom: 1.2, X0: 0, Y0: 0.5, X1: 1, Y1: 0.5}},
		{ID: "c9", MediaID: "narrow", Kind: project.ClipPhoto, Label: "极窄长卷",
			Dwell: 2, Fit: geometry.FitFull},
		{ID: "c10", MediaID: "lost", Kind: project.ClipVideo, Label: "遗失素材",
			In: 0, Out: 2, Fit: geometry.FitFull},
	}
	return p
}

func TestExportBlocksUnresolved(t *testing.T) {
	ffmpeg, ffprobe := toolchain(t)
	assets := assetsDir(t)
	p := buildDemoProject(t, ffprobe, assets)
	p.RecheckMedia(shaOf)

	// The lost reel must be flagged missing but the project still opens.
	m := p.MediaByID("lost")
	if m.Status != project.StatusMissing || m.Reason == "" {
		t.Fatalf("missing media status = %s reason %q", m.Status, m.Reason)
	}

	exp := &Exporter{FFmpeg: ffmpeg, TempDir: t.TempDir()}
	out := filepath.Join(t.TempDir(), "out.mp4")
	_, err := exp.Export(p, Options{OutPath: out, AllowPlaceholder: false})
	if err == nil {
		t.Fatal("export with unresolved media must be blocked")
	}
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
	found := false
	for _, i := range ve.Issues {
		if strings.Contains(i.Message, "遗失素材") || strings.Contains(i.Message, "lost") {
			found = true
		}
	}
	if !found {
		t.Errorf("error must name the unresolved clip: %v", ve.Issues)
	}
	if _, serr := os.Stat(out); serr == nil {
		t.Error("blocked export must not produce output")
	}
}

func TestExportBlocksUnconfirmedCrop(t *testing.T) {
	ffmpeg, ffprobe := toolchain(t)
	assets := assetsDir(t)
	p := project.New("cropcheck", "裁切确认测试")
	p.Media = []project.Media{probeMedia(t, ffprobe, "oldtape", filepath.Join(assets, "oldtape_4x3.mp4"))}
	p.Clips = []project.Clip{
		{ID: "c1", MediaID: "oldtape", Kind: project.ClipVideo, Label: "未确认裁切",
			In: 0, Out: 2, Fit: geometry.FitCrop}, // Crop.Confirmed = false
	}
	exp := &Exporter{FFmpeg: ffmpeg, TempDir: t.TempDir()}
	_, err := exp.Export(p, Options{OutPath: filepath.Join(t.TempDir(), "o.mp4")})
	if err == nil || !strings.Contains(err.Error(), "未经编辑确认") {
		t.Fatalf("unconfirmed crop must block export: %v", err)
	}
}

func TestExportRejectsInvalidPan(t *testing.T) {
	ffmpeg, ffprobe := toolchain(t)
	assets := assetsDir(t)
	p := project.New("pancheck", "平移取景测试")
	p.Media = []project.Media{probeMedia(t, ffprobe, "photo", filepath.Join(assets, "photo_landscape.jpg"))}
	p.Clips = []project.Clip{
		{ID: "c1", MediaID: "photo", Kind: project.ClipPhoto, Label: "越界平移",
			Dwell: 2, Fit: geometry.FitCrop,
			Crop: project.Crop{Confirmed: true},
			Pan:  &geometry.Pan{Zoom: 0.8}}, // zoom < 1: viewport exceeds image
	}
	exp := &Exporter{FFmpeg: ffmpeg, TempDir: t.TempDir()}
	_, err := exp.Export(p, Options{OutPath: filepath.Join(t.TempDir(), "o.mp4")})
	if err == nil || !strings.Contains(err.Error(), "取景范围无效") {
		t.Fatalf("invalid pan must block export: %v", err)
	}
}

func TestExportEndToEnd(t *testing.T) {
	ffmpeg, ffprobe := toolchain(t)
	assets := assetsDir(t)
	p := buildDemoProject(t, ffprobe, assets)
	p.RecheckMedia(shaOf)

	dir := t.TempDir()
	out := filepath.Join(dir, "program.mp4")
	exp := &Exporter{FFmpeg: ffmpeg, TempDir: filepath.Join(dir, "tmp")}
	manifest, err := exp.Export(p, Options{OutPath: out, AllowPlaceholder: true})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if err := WriteManifest(filepath.Join(dir, "manifest.json"), manifest); err != nil {
		t.Fatal(err)
	}

	// --- Container-level checks ---
	v := probeJSON(t, ffprobe, out)
	if got := v.stream("video", "width"); got != "1920" {
		t.Errorf("width = %s", got)
	}
	if got := v.stream("video", "height"); got != "1080" {
		t.Errorf("height = %s", got)
	}
	if got := v.stream("video", "avg_frame_rate"); got != "30/1" {
		t.Errorf("fps = %s", got)
	}
	const wantDur = 3 + 2 + 2 + 2 + 2 + 2 + 2 + 3 + 2 + 2 // 22
	if d := v.formatDuration(); math.Abs(d-wantDur) > 0.35 {
		t.Errorf("video duration = %.3f, want ~%d", d, wantDur)
	}
	if d := v.audioDuration(); math.Abs(d-wantDur) > 0.35 {
		t.Errorf("audio duration = %.3f, want ~%d (audio splice broken)", d, wantDur)
	}

	// --- Frame-level geometry checks ---
	// Segment start times: c1:0 c2:3 c3:5 c4:7 c5:9 c6:11 c7:13 c8:15 c9:18 c10:20
	frame := func(at float64) image.Image {
		t.Helper()
		png := filepath.Join(dir, fmt.Sprintf("f_%.2f.png", at))
		runFFmpeg(t, ffmpeg, "-ss", fmt.Sprintf("%.3f", at), "-i", out,
			"-frames:v", "1", "-y", png)
		return decodeImg(t, png)
	}

	// c1 interview full: 16:9 fills canvas, all corners carry markers.
	f := frame(1.5)
	assertColor(t, f, 30, 30, "red")
	assertColor(t, f, 1889, 30, "green")
	assertColor(t, f, 30, 1049, "yellow")
	assertColor(t, f, 1889, 1049, "blue")
	assertColor(t, f, 960, 6, "magenta")
	assertColor(t, f, 960, 1073, "cyan")

	// c2 oldtape crop-top: cover scale 3, bottom of source cut off.
	f = frame(4.0)
	assertColor(t, f, 30, 30, "red")       // source TL visible
	assertColor(t, f, 960, 6, "magenta")   // top strip visible
	assertNotColor(t, f, 960, 1073, "cyan") // bottom strip cropped away

	// c3 photo full: pillarboxed 1620x1080 centered at x=150.
	f = frame(6.0)
	assertColor(t, f, 10, 540, "black")  // letterbox
	assertColor(t, f, 1909, 540, "black")
	assertColor(t, f, 165, 15, "red")    // source TL marker scaled

	// c4 manuscript border: fg 540x1080 at x=690, red matte padding 10,
	// blurred ivory background at the sides.
	f = frame(8.0)
	assertColor(t, f, 684, 540, "red")   // matte padding
	assertColor(t, f, 705, 15, "red")    // fg TL marker (red on red matte)
	assertNotColor(t, f, 100, 540, "black") // blurred bg, not black

	// c5 rotated phone video: normalized 720x1280 -> 608x1080 at x=656.
	// transpose=2 (CCW): stored TL red -> display BL.
	f = frame(10.0)
	assertColor(t, f, 681, 1055, "red")     // bottom-left of fg
	assertColor(t, f, 662, 540, "magenta")  // stored top strip -> left edge
	assertColor(t, f, 10, 540, "black")     // pillarbox

	// c6 EXIF portrait: normalized 800x1200 -> 720x1080 at x=600.
	// EXIF 6 = transpose=1 (CW): stored TL red -> display TR.
	f = frame(12.0)
	assertColor(t, f, 1293, 27, "red") // top-right of fg
	assertColor(t, f, 10, 540, "black")

	// c7 silent video full: 1280x720 -> 1920x1080 exact.
	f = frame(14.0)
	assertColor(t, f, 30, 30, "red")
	assertColor(t, f, 960, 1073, "cyan")

	// c8 panning photo: viewport must move over the dwell.
	fA := frame(15.3)
	fB := frame(17.7)
	if imagesEqual(fA, fB) {
		t.Error("pan segment: start and end frames identical, pan did not move")
	}

	// c9 extreme narrow strip full: 4000x160 -> 1920x76.8 centered.
	f = frame(19.0)
	assertColor(t, f, 960, 540, "notblack") // strip content crosses center
	assertColor(t, f, 960, 100, "black")    // letterbox above

	// c10 placeholder: red border, dark gray field.
	f = frame(21.0)
	assertColor(t, f, 4, 4, "red")
	assertColor(t, f, 960, 540, "darkgray")

	// --- Audio splice checks ---
	// Interview segment carries the 440Hz tone.
	if rms := audioRMS(t, ffmpeg, out, 0.5, 1.0); rms < -40 {
		t.Errorf("interview segment audio too quiet: %.1f dB", rms)
	}
	// Photo segment must be silent (generated track).
	if rms := audioRMS(t, ffmpeg, out, 6.0, 1.0); rms > -60 {
		t.Errorf("photo segment should be silent: %.1f dB", rms)
	}
	// Silent-video segment must also be silent.
	if rms := audioRMS(t, ffmpeg, out, 14.0, 1.0); rms > -60 {
		t.Errorf("silent-video segment should be silent: %.1f dB", rms)
	}

	// --- Manifest checks ---
	if len(manifest.Clips) != 10 {
		t.Fatalf("manifest clips = %d", len(manifest.Clips))
	}
	for _, c := range manifest.Clips {
		if c.ClipID == "c2" || c.ClipID == "c8" {
			if c.DisplayClaim != "cropped" {
				t.Errorf("%s claim = %s, want cropped", c.ClipID, c.DisplayClaim)
			}
		} else if c.DisplayClaim != "complete" {
			t.Errorf("%s claim = %s, want complete", c.ClipID, c.DisplayClaim)
		}
	}
	if !manifest.Clips[9].Placeholder || manifest.Clips[9].Reason == "" {
		t.Error("c10 must be a placeholder with reason")
	}
	if manifest.Clips[0].SourceSHA256 == "" {
		t.Error("manifest must carry original file hashes")
	}
}

func TestProjectReopen(t *testing.T) {
	_, ffprobe := toolchain(t)
	assets := assetsDir(t)
	dir := t.TempDir()

	p := buildDemoProject(t, ffprobe, assets)
	p.RecheckMedia(shaOf)
	if err := p.Save(dir); err != nil {
		t.Fatal(err)
	}

	// Reopen: clips, media refs and hashes survive the round trip.
	q, err := project.Load(dir, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Clips) != len(p.Clips) || len(q.Media) != len(p.Media) {
		t.Fatalf("reopen lost data: %d clips %d media", len(q.Clips), len(q.Media))
	}
	q.RecheckMedia(shaOf)
	if q.MediaByID("interview").Status != project.StatusOK {
		t.Error("interview should be ok after reopen")
	}
	if q.MediaByID("lost").Status != project.StatusMissing {
		t.Error("lost reel should be missing after reopen")
	}
	// Long title survives save/load.
	if !strings.Contains(q.MediaByID("interview").Title, "李秀兰") {
		t.Error("long title lost in project round trip")
	}

	// Tamper detection: rewrite the file, hash mismatch must surface.
	victim := filepath.Join(dir, "tamper.jpg")
	data, err := os.ReadFile(filepath.Join(assets, "photo_landscape.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(victim, data, 0o644); err != nil {
		t.Fatal(err)
	}
	m := probeMedia(t, ffprobe, "tamper", victim)
	if err := os.WriteFile(victim, append(data, 0x00), 0o644); err != nil {
		t.Fatal(err)
	}
	r := project.New("tampertest", "篡改检测")
	r.Media = []project.Media{m}
	r.RecheckMedia(shaOf)
	if r.MediaByID("tamper").Status != project.StatusChanged {
		t.Errorf("tampered file status = %s", r.MediaByID("tamper").Status)
	}
}

// --- helpers ---

func shaOf(path string) (string, error) {
	info, err := media.NewProber(os.Getenv("ORALCUT_FFPROBE")).Probe(path)
	if err != nil {
		return "", err
	}
	return info.SHA256, nil
}

func runFFmpeg(t *testing.T, ffmpeg string, args ...string) {
	t.Helper()
	cmd := exec.Command(ffmpeg, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg %v: %v\n%s", args, err, out)
	}
}

func decodeImg(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func pixel(img image.Image, x, y int) (r, g, b int) {
	rr, gg, bb, _ := img.At(x, y).RGBA()
	return int(rr >> 8), int(gg >> 8), int(bb >> 8)
}

func assertColor(t *testing.T, img image.Image, x, y int, want string) {
	t.Helper()
	r, g, b := pixel(img, x, y)
	ok := false
	switch want {
	case "red":
		ok = r > 180 && g < 90 && b < 90
	case "green":
		ok = g > 90 && g < 180 && r < 70 && b < 70
	case "blue":
		ok = b > 180 && r < 90 && g < 90
	case "yellow":
		ok = r > 180 && g > 180 && b < 90
	case "magenta":
		ok = r > 180 && b > 180 && g < 90
	case "cyan":
		ok = g > 180 && b > 180 && r < 90
	case "black":
		ok = r < 45 && g < 45 && b < 45
	case "notblack":
		ok = r+g+b > 140
	case "darkgray":
		ok = r > 25 && r < 75 && g > 25 && g < 75 && b > 25 && b < 85
	}
	if !ok {
		t.Errorf("pixel (%d,%d) = #%02x%02x%02x, want %s", x, y, r, g, b, want)
	}
}

func assertNotColor(t *testing.T, img image.Image, x, y int, not string) {
	t.Helper()
	r, g, b := pixel(img, x, y)
	switch not {
	case "cyan":
		if g > 180 && b > 180 && r < 90 {
			t.Errorf("pixel (%d,%d) is cyan, should be cropped away", x, y)
		}
	case "black":
		if r < 45 && g < 45 && b < 45 {
			t.Errorf("pixel (%d,%d) is black, expected blurred background", x, y)
		}
	}
}

func imagesEqual(a, b image.Image) bool {
	if a.Bounds() != b.Bounds() {
		return false
	}
	bnd := a.Bounds()
	for y := bnd.Min.Y; y < bnd.Max.Y; y += 7 {
		for x := bnd.Min.X; x < bnd.Max.X; x += 7 {
			ar, ag, ab, _ := a.At(x, y).RGBA()
			br, bg, bb, _ := b.At(x, y).RGBA()
			if ar != br || ag != bg || ab != bb {
				return false
			}
		}
	}
	return true
}

// audioRMS measures the RMS level (dB) of a time range via ffmpeg astats.
func audioRMS(t *testing.T, ffmpeg, file string, at, dur float64) float64 {
	t.Helper()
	cmd := exec.Command(ffmpeg, "-ss", fmt.Sprintf("%.3f", at), "-t", fmt.Sprintf("%.3f", dur),
		"-i", file, "-map", "a", "-af", "astats", "-f", "null", "-")
	out, _ := cmd.CombinedOutput()
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "RMS level dB") {
			var v float64
			if _, err := fmt.Sscanf(strings.TrimSpace(line[strings.Index(line, ":")+1:]), "%f", &v); err == nil {
				return v
			}
		}
	}
	t.Fatalf("no RMS in astats output:\n%s", out)
	return 0
}

type probeResult struct {
	raw map[string]any
}

func probeJSON(t *testing.T, ffprobe, file string) *probeResult {
	t.Helper()
	cmd := exec.Command(ffprobe, "-v", "error", "-print_format", "json",
		"-show_format", "-show_streams", file)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	return &probeResult{raw: m}
}

func (p *probeResult) stream(codecType, field string) string {
	for _, s := range p.raw["streams"].([]any) {
		sm := s.(map[string]any)
		if sm["codec_type"] == codecType {
			return fmt.Sprint(sm[field])
		}
	}
	return ""
}

func (p *probeResult) formatDuration() float64 {
	var d float64
	fmt.Sscanf(fmt.Sprint(p.raw["format"].(map[string]any)["duration"]), "%f", &d)
	return d
}

func (p *probeResult) audioDuration() float64 {
	for _, s := range p.raw["streams"].([]any) {
		sm := s.(map[string]any)
		if sm["codec_type"] == "audio" {
			var d float64
			fmt.Sscanf(fmt.Sprint(sm["duration"]), "%f", &d)
			return d
		}
	}
	return 0
}
