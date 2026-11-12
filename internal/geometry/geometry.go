// Package geometry defines the single source of truth for how a normalized
// source frame is placed on the output canvas. The Vue preview implements
// the same rules in geometry.js and a parity test keeps them in sync, so
// preview and final render always agree.
package geometry

import (
	"encoding/json"
	"fmt"
	"math"
)

// FitMode selects how a source is composed onto the canvas.
type FitMode string

const (
	// FitFull preserves the entire source, letterboxing as needed.
	FitFull FitMode = "full"
	// FitCrop scales to cover the canvas and crops overflow. The crop
	// position must be confirmed by an editor; nothing is auto-cropped.
	FitCrop FitMode = "crop"
	// FitBorder preserves the entire source over a blurred background
	// fill, producing a background border.
	FitBorder FitMode = "border"
)

// MaxScaledDim caps any scaled source dimension so ffmpeg stays within
// safe memory/frame-size limits.
const MaxScaledDim = 8192

// Rect is an axis-aligned rectangle in pixels.
type Rect struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// Layout is the resolved placement of one source on the canvas.
type Layout struct {
	// Fg is the destination rect of the (normalized) source on the canvas.
	Fg Rect `json:"fg"`
	// SrcVisible is the region of the normalized source (source pixels)
	// that ends up visible. For FitFull/FitBorder it equals the whole
	// source; for FitCrop it is the canvas viewport mapped back.
	SrcVisible Rect `json:"srcVisible"`
	// Scale is the uniform scale applied to the source.
	Scale float64 `json:"scale"`
	// Complete is true when the entire source is visible (FitFull,
	// FitBorder). FitCrop always reports false: a crop never claims
	// complete display.
	Complete bool `json:"complete"`
	// HasBackground is true for FitBorder (blurred fill behind Fg).
	HasBackground bool `json:"hasBackground"`
}

// NormalizedSize returns source dimensions after rotation metadata is
// applied. Rotation must be normalized before any composition math.
func NormalizedSize(w, h, rotation int) (int, int) {
	r := ((rotation % 360) + 360) % 360
	if r == 90 || r == 270 {
		return h, w
	}
	return w, h
}

func coverScale(sw, sh, cw, ch float64) float64 {
	return math.Max(cw/sw, ch/sh)
}

func fitScale(sw, sh, cw, ch float64) float64 {
	return math.Min(cw/sw, ch/sh)
}

// LayoutFor computes the canvas layout for a normalized source of
// sw x sh on a canvas of cw x ch. cropX/cropY in [0,1] position the
// viewport within the overflow for FitCrop (0 = left/top, 1 = right/bottom).
func LayoutFor(sw, sh, cw, ch float64, mode FitMode, cropX, cropY float64) (Layout, error) {
	if sw <= 0 || sh <= 0 || cw <= 0 || ch <= 0 {
		return Layout{}, fmt.Errorf("invalid dimensions: src %vx%v canvas %vx%v", sw, sh, cw, ch)
	}
	switch mode {
	case FitFull, FitBorder:
		s := fitScale(sw, sh, cw, ch)
		fw, fh := sw*s, sh*s
		return Layout{
			Fg:          Rect{X: (cw - fw) / 2, Y: (ch - fh) / 2, W: fw, H: fh},
			SrcVisible:  Rect{X: 0, Y: 0, W: sw, H: sh},
			Scale:       s,
			Complete:    true,
			HasBackground: mode == FitBorder,
		}, nil
	case FitCrop:
		s := coverScale(sw, sh, cw, ch)
		scaledW, scaledH := sw*s, sh*s
		if scaledW > MaxScaledDim || scaledH > MaxScaledDim {
			return Layout{}, fmt.Errorf("crop scale %.4f overflows max scaled dimension (%vx%v > %d)", s, scaledW, scaledH, MaxScaledDim)
		}
		overX := scaledW - cw
		overY := scaledH - ch
		vx := clamp01(cropX) * overX
		vy := clamp01(cropY) * overY
		return Layout{
			Fg:         Rect{X: 0, Y: 0, W: cw, H: ch},
			SrcVisible: Rect{X: vx / s, Y: vy / s, W: cw / s, H: ch / s},
			Scale:      s,
			Complete:   false,
		}, nil
	default:
		return Layout{}, fmt.Errorf("unknown fit mode %q", mode)
	}
}

// Pan describes a slow viewport move across a cover-scaled still photo.
// Positions are normalized [0,1] within the overflow range.
type Pan struct {
	Zoom   float64 `json:"zoom"` // multiplier over cover scale, >= 1
	X0, Y0 float64 `json:"-"`
	X1, Y1 float64 `json:"-"`
}

// MarshalJSON encodes a Pan with explicit field names.
func (p Pan) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Zoom float64 `json:"zoom"`
		X0   float64 `json:"x0"`
		Y0   float64 `json:"y0"`
		X1   float64 `json:"x1"`
		Y1   float64 `json:"y1"`
	}{p.Zoom, p.X0, p.Y0, p.X1, p.Y1})
}

// UnmarshalJSON decodes a Pan with explicit field names.
func (p *Pan) UnmarshalJSON(data []byte) error {
	var v struct {
		Zoom float64 `json:"zoom"`
		X0   float64 `json:"x0"`
		Y0   float64 `json:"y0"`
		X1   float64 `json:"x1"`
		Y1   float64 `json:"y1"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	p.Zoom, p.X0, p.Y0, p.X1, p.Y1 = v.Zoom, v.X0, v.Y0, v.X1, v.Y1
	return nil
}

// PanViewport resolves the pan viewport at progress t in [0,1] for a
// normalized source sw x sh on canvas cw x ch. It returns the viewport
// origin inside the scaled image and the applied scale.
func PanViewport(sw, sh, cw, ch float64, p Pan, t float64) (vx, vy, scale float64, err error) {
	if p.Zoom < 1 {
		return 0, 0, 0, fmt.Errorf("pan zoom %.3f < 1: viewport would exceed image bounds", p.Zoom)
	}
	s := coverScale(sw, sh, cw, ch) * p.Zoom
	scaledW, scaledH := sw*s, sh*s
	if scaledW > MaxScaledDim || scaledH > MaxScaledDim {
		return 0, 0, 0, fmt.Errorf("pan scale %.4f overflows max scaled dimension (%vx%v > %d)", s, scaledW, scaledH, MaxScaledDim)
	}
	// Guarantee the framing range is valid: the viewport must always fit
	// inside the scaled image.
	if scaledW < cw || scaledH < ch {
		return 0, 0, 0, fmt.Errorf("scaled image %vx%v smaller than canvas %vx%v", scaledW, scaledH, cw, ch)
	}
	overX := scaledW - cw
	overY := scaledH - ch
	x0 := clamp01(p.X0) * overX
	y0 := clamp01(p.Y0) * overY
	x1 := clamp01(p.X1) * overX
	y1 := clamp01(p.Y1) * overY
	tt := clamp01(t)
	return x0 + (x1-x0)*tt, y0 + (y1-y0)*tt, s, nil
}

// ValidatePan checks a pan stays within valid framing for its whole range.
func ValidatePan(sw, sh, cw, ch float64, p Pan) error {
	for _, t := range []float64{0, 0.5, 1} {
		if _, _, _, err := PanViewport(sw, sh, cw, ch, p, t); err != nil {
			return err
		}
	}
	return nil
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// Even rounds to the nearest even integer (ffmpeg requires even dims for
// yuv420p). Minimum 2.
func Even(v float64) int {
	n := int(math.Round(v))
	if n%2 != 0 {
		n++
	}
	if n < 2 {
		n = 2
	}
	return n
}
