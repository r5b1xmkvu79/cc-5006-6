package media

import "oralcut/internal/geometry"

// DescriptionVersion is the current unified media description version.
// Version 1 pins stored size, rotation/EXIF, pixel aspect ratio and the
// derived display size in one place; older project files scattered these
// across flat fields and had no pixel ratio at all.
const DescriptionVersion = 1

// Description is the unified, versioned record of how coded pixels become
// displayed pixels. It is the single source of truth for composition:
// coded dimensions are never used as display dimensions.
type Description struct {
	Version int `json:"version"`

	// StoredWidth/Height are the coded dimensions before any normalization.
	StoredWidth  int `json:"storedWidth"`
	StoredHeight int `json:"storedHeight"`

	// Rotation in degrees (0/90/180/270) from the container display matrix
	// or EXIF orientation.
	Rotation int `json:"rotation"`
	// EXIFOrientation is the raw EXIF value (1-8) for images, 0 if none.
	EXIFOrientation int `json:"exifOrientation,omitempty"`

	// SARNum/SARDen are the sample (pixel) aspect ratio; 1:1 for square.
	SARNum int `json:"sarNum"`
	SARDen int `json:"sarDen"`

	// DisplayWidth/Height are the true display dimensions: coded size with
	// pixel aspect ratio and rotation applied.
	DisplayWidth  int `json:"displayWidth"`
	DisplayHeight int `json:"displayHeight"`

	Duration float64 `json:"duration"` // seconds; 0 for stills
	HasAudio bool    `json:"hasAudio"`
}

// DescriptionFromInfo builds the unified description from a probe result.
func DescriptionFromInfo(info *Info) *Description {
	return &Description{
		Version:         DescriptionVersion,
		StoredWidth:     info.StoredWidth,
		StoredHeight:    info.StoredHeight,
		Rotation:        info.Rotation,
		EXIFOrientation: info.EXIFOrientation,
		SARNum:          info.SARNum,
		SARDen:          info.SARDen,
		DisplayWidth:    info.DisplayWidth,
		DisplayHeight:   info.DisplayHeight,
		Duration:        info.Duration,
		HasAudio:        info.HasAudio,
	}
}

// Rederive recomputes the display dimensions from the stored fields and
// checks them against the recorded ones. A mismatch means the description
// was hand-edited or corrupted.
func (d *Description) Rederive() (dw, dh int, consistent bool) {
	dw, dh = geometry.DisplaySize(d.StoredWidth, d.StoredHeight, d.SARNum, d.SARDen, d.Rotation)
	return dw, dh, dw == d.DisplayWidth && dh == d.DisplayHeight
}
