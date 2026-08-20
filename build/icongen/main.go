// Command icongen renders the Forge application icon.
//
// The icon is drawn procedurally rather than rasterized from SVG because no
// SVG rasterizer is guaranteed to be installed; this needs nothing but the Go
// standard library. Shapes are sampled at 4x and box-filtered down, which is
// what keeps the edges clean at 16px.
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
)

const supersample = 4

type rgb struct{ r, g, b float64 }

var (
	bgTop    = rgb{0.13, 0.15, 0.19} // slate, top-left
	bgBottom = rgb{0.04, 0.05, 0.07} // near-black, bottom-right
	markHot  = rgb{1.00, 0.72, 0.23} // amber, heated metal
	markCool = rgb{0.98, 0.45, 0.12} // deeper orange
	ember    = rgb{1.00, 0.34, 0.29}
)

func main() {
	outDir := "build"
	if len(os.Args) > 1 {
		outDir = os.Args[1]
	}
	iconset := filepath.Join(outDir, "Forge.iconset")
	if err := os.MkdirAll(iconset, 0o755); err != nil {
		fail(err)
	}

	// The sizes macOS expects in an .iconset directory.
	for _, spec := range []struct {
		name string
		size int
	}{
		{"icon_16x16.png", 16}, {"icon_16x16@2x.png", 32},
		{"icon_32x32.png", 32}, {"icon_32x32@2x.png", 64},
		{"icon_128x128.png", 128}, {"icon_128x128@2x.png", 256},
		{"icon_256x256.png", 256}, {"icon_256x256@2x.png", 512},
		{"icon_512x512.png", 512}, {"icon_512x512@2x.png", 1024},
	} {
		if err := write(filepath.Join(iconset, spec.name), render(spec.size)); err != nil {
			fail(err)
		}
	}
	// A plain 1024px PNG for Linux and Windows packaging.
	if err := write(filepath.Join(outDir, "icon.png"), render(1024)); err != nil {
		fail(err)
	}
	fmt.Println("wrote", iconset, "and", filepath.Join(outDir, "icon.png"))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "icongen:", err)
	os.Exit(1)
}

func write(path string, img image.Image) (err error) {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
	}()
	return png.Encode(f, img)
}

// render draws the icon at the given pixel size.
func render(size int) *image.RGBA {
	hi := size * supersample
	buf := image.NewRGBA(image.Rect(0, 0, hi, hi))
	u := float64(hi) // work in 0..1 and scale up

	corner := 0.22 * u
	for y := range hi {
		for x := range hi {
			fx, fy := float64(x), float64(y)
			if !insideRounded(fx, fy, u, corner) {
				continue
			}
			// Diagonal gradient across the tile.
			t := (fx/u + fy/u) / 2
			c := lerp(bgTop, bgBottom, t)
			buf.Set(x, y, toNRGBA(c, 1))
		}
	}

	// The mark: a bold F, with the crossbars shading from hot to cool so it
	// reads as worked metal rather than flat text.
	stem := rect{0.36, 0.26, 0.46, 0.76}
	top := rect{0.36, 0.26, 0.70, 0.375}
	mid := rect{0.36, 0.455, 0.645, 0.565}
	for y := range hi {
		for x := range hi {
			fx, fy := float64(x)/u, float64(y)/u
			if !(stem.has(fx, fy) || top.has(fx, fy) || mid.has(fx, fy)) {
				continue
			}
			buf.Set(x, y, toNRGBA(lerp(markHot, markCool, (fy-0.26)/0.5), 1))
		}
	}

	// An ember off the tip of the upper bar.
	cx, cy, r := 0.775*u, 0.315*u, 0.043*u
	for y := range hi {
		for x := range hi {
			dx, dy := float64(x)-cx, float64(y)-cy
			if dx*dx+dy*dy <= r*r {
				buf.Set(x, y, toNRGBA(ember, 1))
			}
		}
	}

	return downsample(buf, size)
}

type rect struct{ x0, y0, x1, y1 float64 }

func (r rect) has(x, y float64) bool { return x >= r.x0 && x < r.x1 && y >= r.y0 && y < r.y1 }

// insideRounded reports whether a point falls inside a rounded square that
// fills the tile.
func insideRounded(x, y, size, corner float64) bool {
	if x < 0 || y < 0 || x >= size || y >= size {
		return false
	}
	cx, cy := x, y
	switch {
	case x < corner:
		cx = corner
	case x > size-corner:
		cx = size - corner
	}
	switch {
	case y < corner:
		cy = corner
	case y > size-corner:
		cy = size - corner
	}
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= corner*corner
}

func lerp(a, b rgb, t float64) rgb {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	return rgb{a.r + (b.r-a.r)*t, a.g + (b.g-a.g)*t, a.b + (b.b-a.b)*t}
}

func toNRGBA(c rgb, a float64) color.NRGBA {
	clamp := func(v float64) uint8 {
		if v <= 0 {
			return 0
		}
		if v >= 1 {
			return 255
		}
		return uint8(v*255 + 0.5)
	}
	return color.NRGBA{R: clamp(c.r), G: clamp(c.g), B: clamp(c.b), A: clamp(a)}
}

// downsample box-filters the supersampled buffer, averaging in premultiplied
// space so transparent edge pixels do not darken the result.
func downsample(src *image.RGBA, size int) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, size, size))
	n := float64(supersample * supersample)
	for y := range size {
		for x := range size {
			var r, g, b, a float64
			for sy := range supersample {
				for sx := range supersample {
					c := src.RGBAAt(x*supersample+sx, y*supersample+sy)
					r += float64(c.R)
					g += float64(c.G)
					b += float64(c.B)
					a += float64(c.A)
				}
			}
			out.SetRGBA(x, y, color.RGBA{
				R: uint8(r/n + 0.5), G: uint8(g/n + 0.5),
				B: uint8(b/n + 0.5), A: uint8(a/n + 0.5),
			})
		}
	}
	return out
}
