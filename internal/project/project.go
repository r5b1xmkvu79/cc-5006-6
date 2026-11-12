// Package project defines the edit decision list: clips on a timeline,
// their composition parameters, and media references with integrity
// status. A project always opens even when sources are missing; export
// validation decides what is allowed to render.
package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"oralcut/internal/geometry"
	"oralcut/internal/media"
)

// FormatVersionCurrent is the project file format written by this build.
// Version 1 (legacy) scattered aspect/rotation across flat media fields
// and had no pixel ratio; version 2 adds the unified versioned media
// description per media entry.
const FormatVersionCurrent = 2

// MediaStatus is the integrity state of a referenced source file.
type MediaStatus string

const (
	StatusOK      MediaStatus = "ok"
	StatusMissing MediaStatus = "missing"
	StatusChanged MediaStatus = "changed"
)

// Media is one referenced original. Path + SHA256 pin the original file;
// the project keeps references, never copies, and thumbnails are never a
// source for final frames.
type Media struct {
	ID     string `json:"id"`
	Path   string `json:"path"` // absolute path to the original
	SHA256 string `json:"sha256"`

	Kind     string  `json:"kind"` // "video" | "image"
	Width    int     `json:"width"`  // display dimensions; on legacy (v1) files these were
	Height   int     `json:"height"` // post-rotation coded dimensions with square pixels assumed
	Rotation int     `json:"rotation"`
	// EXIFOrientation preserves the raw EXIF tag for images so the
	// renderer can apply the exact normalization transform.
	EXIFOrientation int     `json:"exifOrientation,omitempty"`
	Duration float64 `json:"duration"`
	HasAudio bool    `json:"hasAudio"`
	Title    string  `json:"title,omitempty"`

	// Description is the unified versioned media description (format v2).
	// When present it is the source of truth for display geometry; the
	// flat fields above mirror it for older readers.
	Description *media.Description `json:"description,omitempty"`

	Status MediaStatus `json:"status"`
	// Reason explains missing/changed status; kept so the project opens
	// with a meaningful placeholder.
	Reason string `json:"reason,omitempty"`
}

// DisplayDims returns the true display dimensions. The unified
// description wins when present; legacy flat fields are the fallback for
// pre-migration (v1) projects, where they recorded post-rotation coded
// dimensions under a square-pixel assumption.
func (m *Media) DisplayDims() (int, int) {
	if m.Description != nil && m.Description.DisplayWidth > 0 && m.Description.DisplayHeight > 0 {
		return m.Description.DisplayWidth, m.Description.DisplayHeight
	}
	return m.Width, m.Height
}

// ClipKind distinguishes how time is modeled for a clip.
type ClipKind string

const (
	ClipVideo ClipKind = "video" // time from In/Out trim of the source
	ClipPhoto ClipKind = "photo" // time from Dwell, independent of source
)

// Crop holds the editor-confirmed crop viewport position.
type Crop struct {
	X float64 `json:"x"` // 0..1 across horizontal overflow
	Y float64 `json:"y"` // 0..1 across vertical overflow
	// Confirmed must be set by a human editor before export. Without it
	// the project refuses to render a crop, so nothing is silently cut.
	Confirmed   bool   `json:"confirmed"`
	ConfirmedBy string `json:"confirmedBy,omitempty"`
}

// Border configures the blurred-background border presentation.
type Border struct {
	Blur    int    `json:"blur"`    // background blur radius
	Padding int    `json:"padding"` // extra matte around foreground, px
	Color   string `json:"color"`   // matte color when padding > 0
}

// Clip is one timeline segment.
type Clip struct {
	ID      string   `json:"id"`
	MediaID string   `json:"mediaId"`
	Kind    ClipKind `json:"kind"`
	Label   string   `json:"label"`

	// Video only: source trim in seconds.
	In  float64 `json:"in,omitempty"`
	Out float64 `json:"out,omitempty"`

	// Photo only: on-screen dwell in seconds.
	Dwell float64 `json:"dwell,omitempty"`

	Fit    geometry.FitMode `json:"fit"`
	Crop   Crop             `json:"crop,omitempty"`
	Border Border           `json:"border,omitempty"`
	// Pan is an optional slow move for photos in crop fit.
	Pan    *geometry.Pan `json:"pan,omitempty"`
}

// Duration returns the clip's timeline length in seconds.
func (c Clip) Duration() float64 {
	if c.Kind == ClipPhoto {
		return c.Dwell
	}
	return c.Out - c.In
}

// DisplayClaim states whether the clip shows its source completely.
// Crop mode can never claim complete display.
func (c Clip) DisplayClaim() string {
	if c.Fit == geometry.FitCrop {
		return "cropped"
	}
	return "complete"
}

// Canvas is the unified output frame.
type Canvas struct {
	Width  int     `json:"width"`
	Height int     `json:"height"`
	FPS    int     `json:"fps"`
	Background string `json:"background"`
}

// Project is the whole edit.
type Project struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	// FormatVersion is absent/1 on legacy files, 2 once the unified media
	// description is present.
	FormatVersion int            `json:"formatVersion,omitempty"`
	Canvas    Canvas   `json:"canvas"`
	Media     []Media  `json:"media"`
	Clips     []Clip   `json:"clips"`
	CreatedAt string   `json:"createdAt"`
	UpdatedAt string   `json:"updatedAt"`
	// Legacy preserves the pre-migration envelope (original unknown fields,
	// overridden clip confirmations) so a rollback can restore the old
	// format faithfully. Written by the migration tool, carried through
	// app saves untouched.
	Legacy json.RawMessage `json:"legacy,omitempty"`
}

func New(id, title string) *Project {
	now := time.Now().UTC().Format(time.RFC3339)
	return &Project{
		ID:    id,
		Title: title,
		FormatVersion: FormatVersionCurrent,
		Canvas: Canvas{Width: 1920, Height: 1080, FPS: 30, Background: "black"},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// MediaByID looks up a media reference.
func (p *Project) MediaByID(id string) *Media {
	for i := range p.Media {
		if p.Media[i].ID == id {
			return &p.Media[i]
		}
	}
	return nil
}

// Save writes the project JSON.
func (p *Project) Save(dir string) error {
	p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	p.NormalizeForSave()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, p.ID+".json"), data, 0o644)
}

// NormalizeForSave mirrors the unified media description into the flat
// compat fields and bumps the format version once any description exists.
// It does not touch timestamps so the migration tool can produce files
// without save side effects.
func (p *Project) NormalizeForSave() {
	hasDesc := false
	for i := range p.Media {
		m := &p.Media[i]
		if m.Description == nil {
			continue
		}
		hasDesc = true
		d := m.Description
		m.Width, m.Height = d.DisplayWidth, d.DisplayHeight
		m.Rotation = d.Rotation
		m.EXIFOrientation = d.EXIFOrientation
		m.Duration = d.Duration
		m.HasAudio = d.HasAudio
	}
	if hasDesc && p.FormatVersion < FormatVersionCurrent {
		p.FormatVersion = FormatVersionCurrent
	}
}

// Load reads a project file. It never fails because a source file is
// gone: media status is refreshed separately via RecheckMedia.
func Load(dir, id string) (*Project, error) {
	data, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if err != nil {
		return nil, err
	}
	var p Project
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("project file corrupt: %w", err)
	}
	return &p, nil
}

// RecheckMedia refreshes every media status against the filesystem.
// Missing files keep their slot and get an explicit reason.
func (p *Project) RecheckMedia(hashFn func(path string) (string, error)) {
	for i := range p.Media {
		m := &p.Media[i]
		sum, err := hashFn(m.Path)
		switch {
		case err != nil:
			m.Status = StatusMissing
			m.Reason = fmt.Sprintf("源文件缺失: %s", m.Path)
		case m.SHA256 != "" && sum != m.SHA256:
			m.Status = StatusChanged
			m.Reason = "源文件内容已变化（哈希不一致）"
		default:
			m.Status = StatusOK
			m.Reason = ""
		}
	}
}

// Issue is one validation finding.
type Issue struct {
	ClipID  string `json:"clipId,omitempty"`
	MediaID string `json:"mediaId,omitempty"`
	Level   string `json:"level"` // "error" blocks export, "warning" does not
	Message string `json:"message"`
}

// Validate checks the project for export readiness. When allowPlaceholder
// is false, unresolved (missing/changed) sources are errors; otherwise
// they degrade to warnings and render as placeholder slates.
func (p *Project) Validate(allowPlaceholder bool) []Issue {
	var issues []Issue
	for _, c := range p.Clips {
		m := p.MediaByID(c.MediaID)
		if m == nil {
			issues = append(issues, Issue{ClipID: c.ID, Level: "error",
				Message: fmt.Sprintf("片段 %s 引用了不存在的素材 %s", c.ID, c.MediaID)})
			continue
		}
		if m.Status != StatusOK {
			level := "error"
			if allowPlaceholder {
				level = "warning"
			}
			issues = append(issues, Issue{ClipID: c.ID, MediaID: m.ID, Level: level,
				Message: fmt.Sprintf("片段 %s 的素材未解决: %s", c.Label, m.Reason)})
		}
		if c.Duration() <= 0 {
			issues = append(issues, Issue{ClipID: c.ID, Level: "error",
				Message: fmt.Sprintf("片段 %s 时长无效 (%.2fs)", c.Label, c.Duration())})
		}
		if c.Kind == ClipVideo && m.Status == StatusOK {
			if c.In < 0 || c.Out > m.Duration+0.001 || c.In >= c.Out {
				issues = append(issues, Issue{ClipID: c.ID, Level: "error",
					Message: fmt.Sprintf("片段 %s 取段 [%.2f, %.2f] 超出素材时长 %.2fs", c.Label, c.In, c.Out, m.Duration)})
			}
		}
		if c.Kind == ClipPhoto && (c.Dwell <= 0 || c.Dwell > 600) {
			issues = append(issues, Issue{ClipID: c.ID, Level: "error",
				Message: fmt.Sprintf("片段 %s 照片停留时长 %.2fs 超出允许范围", c.Label, c.Dwell)})
		}
		if c.Fit == geometry.FitCrop && !c.Crop.Confirmed {
			issues = append(issues, Issue{ClipID: c.ID, Level: "error",
				Message: fmt.Sprintf("片段 %s 使用裁切填充但裁切位置未经编辑确认", c.Label)})
		}
		if m.Description != nil {
			if _, _, ok := m.Description.Rederive(); !ok {
				issues = append(issues, Issue{ClipID: c.ID, MediaID: m.ID, Level: "error",
					Message: fmt.Sprintf("片段 %s 的媒体描述内部不一致（显示尺寸与编码/像素比例推导不符）", c.Label)})
			}
		}
		if m.Status == StatusOK {
			dw, dh := m.DisplayDims()
			if _, err := geometry.LayoutFor(float64(dw), float64(dh),
				float64(p.Canvas.Width), float64(p.Canvas.Height), c.Fit, c.Crop.X, c.Crop.Y); err != nil {
				issues = append(issues, Issue{ClipID: c.ID, Level: "error",
					Message: fmt.Sprintf("片段 %s 构图无效: %v", c.Label, err)})
			}
			if c.Pan != nil {
				if c.Kind != ClipPhoto || c.Fit != geometry.FitCrop {
					issues = append(issues, Issue{ClipID: c.ID, Level: "error",
						Message: fmt.Sprintf("片段 %s 平移仅适用于裁切模式的照片", c.Label)})
				} else if err := geometry.ValidatePan(float64(dw), float64(dh),
					float64(p.Canvas.Width), float64(p.Canvas.Height), *c.Pan); err != nil {
					issues = append(issues, Issue{ClipID: c.ID, Level: "error",
						Message: fmt.Sprintf("片段 %s 平移取景范围无效: %v", c.Label, err)})
				}
			}
		}
	}
	return issues
}

// HasErrors reports whether any issue blocks export.
func HasErrors(issues []Issue) bool {
	for _, i := range issues {
		if i.Level == "error" {
			return true
		}
	}
	return false
}
