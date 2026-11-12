package media

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testProber(t *testing.T) *Prober {
	t.Helper()
	ffprobe := os.Getenv("ORALCUT_FFPROBE")
	if ffprobe == "" {
		t.Skip("ORALCUT_FFPROBE not set")
	}
	return NewProber(ffprobe)
}

func assetPath(t *testing.T, name string) string {
	t.Helper()
	dir := os.Getenv("ORALCUT_ASSETS")
	if dir == "" {
		dir = "../../assets"
	}
	p := filepath.Join(dir, name)
	if _, err := os.Stat(p); err != nil {
		t.Skipf("asset %s not generated: %v", name, err)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestProbeInterview(t *testing.T) {
	info, err := testProber(t).Probe(assetPath(t, "interview_16x9.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Kind != KindVideo || info.Width != 1920 || info.Height != 1080 {
		t.Errorf("got %s %dx%d", info.Kind, info.Width, info.Height)
	}
	if !info.HasAudio {
		t.Error("interview must have audio")
	}
	if info.Duration < 7.9 || info.Duration > 8.1 {
		t.Errorf("duration = %v", info.Duration)
	}
	// Long CJK title must survive probing intact.
	if len([]rune(info.Title)) < 100 || !strings.Contains(info.Title, "李秀兰") ||
		!strings.Contains(info.Title, "不得截断") {
		t.Errorf("long title not preserved (%d runes): %q", len([]rune(info.Title)), info.Title)
	}
	if len(info.SHA256) != 64 {
		t.Errorf("sha256 missing: %q", info.SHA256)
	}
}

func TestProbeOldTape(t *testing.T) {
	info, err := testProber(t).Probe(assetPath(t, "oldtape_4x3.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Width != 640 || info.Height != 480 {
		t.Errorf("got %dx%d", info.Width, info.Height)
	}
}

func TestProbeRotatedVideo(t *testing.T) {
	info, err := testProber(t).Probe(assetPath(t, "phone_rotated.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Rotation != 90 {
		t.Errorf("rotation = %d, want 90", info.Rotation)
	}
	// Rotation normalized before composition: stored 1280x720 -> 720x1280.
	if info.Width != 720 || info.Height != 1280 {
		t.Errorf("normalized = %dx%d, want 720x1280", info.Width, info.Height)
	}
	if info.StoredWidth != 1280 || info.StoredHeight != 720 {
		t.Errorf("stored = %dx%d", info.StoredWidth, info.StoredHeight)
	}
}

func TestProbeEXIFPortrait(t *testing.T) {
	info, err := testProber(t).Probe(assetPath(t, "photo_portrait_exif6.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Kind != KindImage {
		t.Errorf("kind = %s", info.Kind)
	}
	if info.EXIFOrientation != 6 {
		t.Errorf("exif = %d, want 6", info.EXIFOrientation)
	}
	if info.Rotation != 90 {
		t.Errorf("rotation = %d, want 90", info.Rotation)
	}
	if info.Width != 800 || info.Height != 1200 {
		t.Errorf("normalized = %dx%d, want 800x1200", info.Width, info.Height)
	}
}

func TestProbeNoAudio(t *testing.T) {
	info, err := testProber(t).Probe(assetPath(t, "noaudio_16x9.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	if info.HasAudio {
		t.Error("noaudio file reported audio")
	}
}

func TestProbeExtremeNarrow(t *testing.T) {
	info, err := testProber(t).Probe(assetPath(t, "extreme_narrow.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Width != 4000 || info.Height != 160 {
		t.Errorf("got %dx%d", info.Width, info.Height)
	}
}

func TestProbeMissing(t *testing.T) {
	_, err := testProber(t).Probe("/nonexistent/file.mp4")
	if err == nil {
		t.Fatal("missing file must error")
	}
}

func TestProbeAnamorphicDV(t *testing.T) {
	info, err := testProber(t).Probe(assetPath(t, "dv_anamorphic.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	if info.StoredWidth != 720 || info.StoredHeight != 576 {
		t.Errorf("stored = %dx%d, want 720x576", info.StoredWidth, info.StoredHeight)
	}
	if info.SARNum != 64 || info.SARDen != 45 {
		t.Errorf("sar = %d:%d, want 64:45", info.SARNum, info.SARDen)
	}
	// Coded dimensions are not display dimensions: 720x576 with 64:45
	// pixels displays as 1024x576.
	if info.DisplayWidth != 1024 || info.DisplayHeight != 576 {
		t.Errorf("display = %dx%d, want 1024x576", info.DisplayWidth, info.DisplayHeight)
	}
	// Normalized coded dims stay 720x576 (rotation 0).
	if info.Width != 720 || info.Height != 576 {
		t.Errorf("normalized coded = %dx%d, want 720x576", info.Width, info.Height)
	}
}

func TestParseSAR(t *testing.T) {
	cases := []struct {
		in    string
		n, d  int
	}{
		{"64:45", 64, 45},
		{"1:1", 1, 1},
		{"0:1", 1, 1},   // unknown -> square
		{"", 1, 1},      // missing -> square
		{"N/A", 1, 1},   // malformed -> square
		{"4:0", 1, 1},   // zero den -> square
	}
	for _, c := range cases {
		n, d := parseSAR(c.in)
		if n != c.n || d != c.d {
			t.Errorf("parseSAR(%q) = %d:%d, want %d:%d", c.in, n, d, c.n, c.d)
		}
	}
}

func TestEXIFRoundTrip(t *testing.T) {
	// Minimal JPEG: SOI + EOI.
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xD9}
	for orient := 1; orient <= 8; orient++ {
		data, err := SetEXIFOrientation(jpeg, orient)
		if err != nil {
			t.Fatal(err)
		}
		got, err := exifOrientationFromJPEG(data)
		if err != nil {
			t.Fatalf("orient %d: %v", orient, err)
		}
		if got != orient {
			t.Errorf("orient %d -> %d", orient, got)
		}
	}
}
