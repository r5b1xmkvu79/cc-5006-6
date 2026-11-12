// Package media probes source files with ffprobe and normalizes rotation
// metadata before any composition parameter is applied.
package media

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Kind classifies a probed source.
type Kind string

const (
	KindVideo Kind = "video"
	KindImage Kind = "image"
)

// Info is the probed, rotation-normalized description of one source file.
type Info struct {
	Path  string `json:"path"`
	SHA256 string `json:"sha256"`
	Kind  Kind   `json:"kind"`

	// StoredWidth/Height are the coded dimensions before rotation.
	StoredWidth  int `json:"storedWidth"`
	StoredHeight int `json:"storedHeight"`
	// Rotation in degrees (0/90/180/270) from container display matrix or
	// EXIF orientation. It is normalized first: Width/Height below already
	// have it applied.
	Rotation int `json:"rotation"`
	// EXIFOrientation is the raw EXIF value (1-8) for images, 0 if none.
	EXIFOrientation int `json:"exifOrientation,omitempty"`

	// Width/Height are the normalized display dimensions.
	Width  int `json:"width"`
	Height int `json:"height"`

	Duration  float64 `json:"duration"` // seconds; 0 for stills
	HasAudio  bool    `json:"hasAudio"`
	Title     string  `json:"title,omitempty"`
	FormatName string `json:"formatName"`
}

// Prober locates the ffmpeg toolchain.
type Prober struct {
	FFProbe string
}

func NewProber(ffprobe string) *Prober { return &Prober{FFProbe: ffprobe} }

type ffprobeOutput struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		Duration  string `json:"duration"`
		Tags      map[string]string `json:"tags"`
		SideData  []struct {
			Type     string  `json:"side_data_type"`
			Rotation float64 `json:"rotation"`
		} `json:"side_data_list"`
	} `json:"streams"`
	Format struct {
		Duration   string            `json:"duration"`
		FormatName string            `json:"format_name"`
		Tags       map[string]string `json:"tags"`
	} `json:"format"`
}

// Probe inspects one file and returns its normalized Info.
func (p *Prober) Probe(path string) (*Info, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("source missing: %w", err)
	}
	cmd := exec.Command(p.FFProbe,
		"-v", "error", "-print_format", "json",
		"-show_format", "-show_streams", path)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe failed for %s: %w", path, err)
	}
	var fp ffprobeOutput
	if err := json.Unmarshal(out, &fp); err != nil {
		return nil, fmt.Errorf("ffprobe output unparseable for %s: %w", path, err)
	}

	info := &Info{Path: path, FormatName: fp.Format.FormatName}
	var vstr *struct {
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		Duration  string `json:"duration"`
		Tags      map[string]string `json:"tags"`
		SideData  []struct {
			Type     string  `json:"side_data_type"`
			Rotation float64 `json:"rotation"`
		} `json:"side_data_list"`
	}
	for i := range fp.Streams {
		s := &fp.Streams[i]
		switch s.CodecType {
		case "video":
			if vstr == nil && s.CodecName != "attached_pic" {
				vstr = s
			}
		case "audio":
			info.HasAudio = true
		}
	}
	if vstr == nil {
		return nil, fmt.Errorf("no video/image stream in %s", path)
	}
	info.StoredWidth, info.StoredHeight = vstr.Width, vstr.Height

	// Rotation: container display matrix first.
	rot := 0
	for _, sd := range vstr.SideData {
		if sd.Type == "Display Matrix" || sd.Type == "displaymatrix" {
			rot = normalizeDegrees(int(sd.Rotation))
		}
	}

	// Still image? Then fall back to EXIF orientation.
	isImage := fp.Format.FormatName == "image2" || fp.Format.FormatName == "image2pipe" ||
		strings.Contains(fp.Format.FormatName, "image2")
	if isImage {
		info.Kind = KindImage
		if rot == 0 {
			if exif, err := EXIFOrientation(path); err == nil && exif >= 1 && exif <= 8 {
				info.EXIFOrientation = exif
				rot = exifRotation(exif)
			}
		}
	} else {
		info.Kind = KindVideo
	}
	info.Rotation = rot
	w, h := normalizedDims(info.StoredWidth, info.StoredHeight, rot, info.EXIFOrientation)
	info.Width, info.Height = w, h

	info.Duration = parseFloat(fp.Format.Duration)
	if info.Duration == 0 && vstr.Duration != "" {
		info.Duration = parseFloat(vstr.Duration)
	}
	if fp.Format.Tags != nil {
		for k, v := range fp.Format.Tags {
			if strings.EqualFold(k, "title") {
				info.Title = v
			}
		}
	}

	sum, err := sha256File(path)
	if err != nil {
		return nil, err
	}
	info.SHA256 = sum
	return info, nil
}

func normalizeDegrees(d int) int {
	r := ((d % 360) + 360) % 360
	switch r {
	case 90, 180, 270:
		return r
	}
	return 0
}

// exifRotation maps EXIF orientation (1-8) to display rotation degrees.
func exifRotation(o int) int {
	switch o {
	case 3:
		return 180
	case 6:
		return 90
	case 8:
		return 270
	}
	return 0
}

// normalizedDims applies rotation/EXIF to stored dimensions. EXIF 5 and 7
// also swap axes.
func normalizedDims(w, h, rot, exif int) (int, int) {
	if rot == 90 || rot == 270 || exif == 5 || exif == 7 {
		return h, w
	}
	return w, h
}

func parseFloat(s string) float64 {
	var f float64
	fmt.Sscanf(s, "%f", &f)
	return f
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Abs resolves path to an absolute path for stable references.
func Abs(path string) (string, error) {
	return filepath.Abs(path)
}
