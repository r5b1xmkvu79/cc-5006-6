package render

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// PlaceholderPNG renders a slate for an unresolved clip: dark field, red
// border, and the media id plus short reason. Generated deterministically
// so export never depends on system fonts. The full reason text lives in
// the export manifest.
func PlaceholderPNG(path, mediaID, reason string, w, h int) error {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	bg := color.RGBA{0x2b, 0x2b, 0x30, 0xff}
	fg := color.RGBA{0xff, 0xff, 0xff, 0xff}
	red := color.RGBA{0xd8, 0x3a, 0x3a, 0xff}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, bg)
		}
	}
	b := 8
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if x < b || x >= w-b || y < b || y >= h-b {
				img.SetRGBA(x, y, red)
			}
		}
	}
	lines := []string{"MISSING MEDIA", "id: " + mediaID}
	if reason != "" {
		r := []rune(reason)
		if len(r) > 40 {
			r = append(r[:40], '.', '.', '.')
		}
		lines = append(lines, string(r))
	}
	lines = append(lines, "see export manifest")
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(fg),
		Face: basicfont.Face7x13,
	}
	y := h/2 - len(lines)*16/2
	for _, ln := range lines {
		d.Dot = fixed.P(24, y)
		d.DrawString(ln)
		y += 20
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}
