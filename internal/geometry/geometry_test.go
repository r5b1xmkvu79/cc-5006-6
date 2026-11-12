package geometry

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func almost(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestNormalizedSize(t *testing.T) {
	cases := []struct{ w, h, rot, ew, eh int }{
		{1920, 1080, 0, 1920, 1080},
		{1920, 1080, 90, 1080, 1920},
		{1920, 1080, 180, 1920, 1080},
		{1920, 1080, 270, 1080, 1920},
		{1280, 720, -90, 720, 1280},
		{1280, 720, 450, 720, 1280},
	}
	for _, c := range cases {
		w, h := NormalizedSize(c.w, c.h, c.rot)
		if w != c.ew || h != c.eh {
			t.Errorf("NormalizedSize(%d,%d,%d) = %dx%d, want %dx%d", c.w, c.h, c.rot, w, h, c.ew, c.eh)
		}
	}
}

func TestLayoutFullPreservesEverything(t *testing.T) {
	// 4:3 source on 16:9 canvas.
	l, err := LayoutFor(640, 480, 1920, 1080, FitFull, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !l.Complete {
		t.Error("FitFull must claim complete display")
	}
	if !almost(l.Fg.H, 1080) || !almost(l.Fg.W, 1440) || !almost(l.Fg.X, 240) || !almost(l.Fg.Y, 0) {
		t.Errorf("unexpected fg rect %+v", l.Fg)
	}
	if l.SrcVisible != (Rect{0, 0, 640, 480}) {
		t.Errorf("full mode must show entire source, got %+v", l.SrcVisible)
	}
}

func TestLayoutBorderIsCompleteWithBackground(t *testing.T) {
	l, err := LayoutFor(1200, 2400, 1920, 1080, FitBorder, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !l.Complete || !l.HasBackground {
		t.Errorf("border mode: complete=%v bg=%v", l.Complete, l.HasBackground)
	}
	if !almost(l.Fg.H, 1080) || !almost(l.Fg.W, 540) || !almost(l.Fg.X, 690) {
		t.Errorf("unexpected fg rect %+v", l.Fg)
	}
}

func TestLayoutCropNeverClaimsComplete(t *testing.T) {
	// 16:9 interview cropped into 16:9 canvas: no overflow.
	l, err := LayoutFor(1920, 1080, 1920, 1080, FitCrop, 0.5, 0.5)
	if err != nil {
		t.Fatal(err)
	}
	if l.Complete {
		t.Error("FitCrop must never claim complete display")
	}
	if !almost(l.SrcVisible.W, 1920) || !almost(l.SrcVisible.H, 1080) {
		t.Errorf("same-aspect crop should show everything: %+v", l.SrcVisible)
	}

	// 4:3 source cropped to 16:9 canvas: vertical overflow.
	l, err = LayoutFor(640, 480, 1920, 1080, FitCrop, 0.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	s := 1920.0 / 640.0 // cover scale = 3
	if !almost(l.Scale, s) {
		t.Errorf("scale = %v, want %v", l.Scale, s)
	}
	// cropY=0 -> top aligned; visible height = 1080/3 = 360 of 480.
	if !almost(l.SrcVisible.Y, 0) || !almost(l.SrcVisible.H, 360) {
		t.Errorf("cropY=0 visible = %+v", l.SrcVisible)
	}
	// cropY=1 -> bottom aligned.
	l, _ = LayoutFor(640, 480, 1920, 1080, FitCrop, 0.5, 1)
	if !almost(l.SrcVisible.Y, 120) || !almost(l.SrcVisible.H, 360) {
		t.Errorf("cropY=1 visible = %+v", l.SrcVisible)
	}
}

func TestCropRejectsExtremeAspectOverflow(t *testing.T) {
	// 4000x160 strip: cover scale for 1080 canvas is 6.75 -> 27000px wide.
	_, err := LayoutFor(4000, 160, 1920, 1080, FitCrop, 0.5, 0.5)
	if err == nil {
		t.Fatal("extreme narrow image must be rejected in crop mode")
	}
	// Full mode still works.
	if _, err := LayoutFor(4000, 160, 1920, 1080, FitFull, 0, 0); err != nil {
		t.Fatalf("full mode must accept extreme narrow image: %v", err)
	}
}

func TestPanViewportValidity(t *testing.T) {
	// 3000x2000 photo on 1920x1080 canvas.
	p := Pan{Zoom: 1.2, X0: 0, Y0: 0, X1: 1, Y1: 1}
	if err := ValidatePan(3000, 2000, 1920, 1080, p); err != nil {
		t.Fatalf("valid pan rejected: %v", err)
	}
	vx0, vy0, s, err := PanViewport(3000, 2000, 1920, 1080, p, 0)
	if err != nil {
		t.Fatal(err)
	}
	if vx0 != 0 || vy0 != 0 {
		t.Errorf("t=0 viewport = %v,%v", vx0, vy0)
	}
	vx1, vy1, _, _ := PanViewport(3000, 2000, 1920, 1080, p, 1)
	scaledW, scaledH := 3000*s, 2000*s
	if !almost(vx1, scaledW-1920) || !almost(vy1, scaledH-1080) {
		t.Errorf("t=1 viewport = %v,%v (scaled %vx%v)", vx1, vy1, scaledW, scaledH)
	}
	// Zoom below 1 is invalid: viewport would exceed image bounds.
	if err := ValidatePan(3000, 2000, 1920, 1080, Pan{Zoom: 0.9}); err == nil {
		t.Error("zoom < 1 must be rejected")
	}
	// Zoom so large the scaled image exceeds limits must be rejected.
	if err := ValidatePan(3000, 2000, 1920, 1080, Pan{Zoom: 40}); err == nil {
		t.Error("zoom overflowing MaxScaledDim must be rejected")
	}
}

// parityCase is one row of the golden fixture shared with geometry.js.
type parityCase struct {
	Name   string  `json:"name"`
	SW     float64 `json:"sw"`
	SH     float64 `json:"sh"`
	CW     float64 `json:"cw"`
	CH     float64 `json:"ch"`
	Mode   FitMode `json:"mode"`
	CropX  float64 `json:"cropX"`
	CropY  float64 `json:"cropY"`
	Layout Layout  `json:"layout"`
	Err    string  `json:"err"`
}

// TestParityFixture regenerates web/test/geometry_cases.json. The JS
// geometry implementation must produce identical results for every case.
func TestParityFixture(t *testing.T) {
	type input struct {
		Name           string
		SW, SH, CW, CH float64
		Mode           FitMode
		CropX, CropY   float64
	}
	inputs := []input{
		{"interview_16x9_full", 1920, 1080, 1920, 1080, FitFull, 0, 0},
		{"oldtape_4x3_full", 640, 480, 1920, 1080, FitFull, 0, 0},
		{"oldtape_4x3_crop_top", 640, 480, 1920, 1080, FitCrop, 0.5, 0},
		{"oldtape_4x3_crop_bottom", 640, 480, 1920, 1080, FitCrop, 0.5, 1},
		{"oldtape_4x3_crop_left_quarter", 640, 480, 1920, 1080, FitCrop, 0.25, 0.75},
		{"manuscript_vertical_border", 1200, 2400, 1920, 1080, FitBorder, 0, 0},
		{"manuscript_vertical_crop", 1200, 2400, 1920, 1080, FitCrop, 0.5, 0.2},
		{"photo_landscape_full", 3000, 2000, 1920, 1080, FitFull, 0, 0},
		{"photo_landscape_crop", 3000, 2000, 1920, 1080, FitCrop, 0.1, 0.9},
		{"portrait_rotated_full", 1080, 1920, 1920, 1080, FitFull, 0, 0},
		{"extreme_narrow_full", 4000, 160, 1920, 1080, FitFull, 0, 0},
		{"extreme_narrow_crop", 4000, 160, 1920, 1080, FitCrop, 0.5, 0.5},
		{"square_full", 1000, 1000, 1920, 1080, FitFull, 0, 0},
		{"square_border", 1000, 1000, 1920, 1080, FitBorder, 0, 0},
	}
	var cases []parityCase
	for _, in := range inputs {
		l, err := LayoutFor(in.SW, in.SH, in.CW, in.CH, in.Mode, in.CropX, in.CropY)
		c := parityCase{
			Name: in.Name, SW: in.SW, SH: in.SH, CW: in.CW, CH: in.CH,
			Mode: in.Mode, CropX: in.CropX, CropY: in.CropY, Layout: l,
		}
		if err != nil {
			c.Err = err.Error()
		}
		cases = append(cases, c)
	}
	data, err := json.MarshalIndent(cases, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("..", "..", "web", "test", "geometry_cases.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
