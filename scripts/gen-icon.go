//go:build ignore

// gen-icon renders the layered dialogue mark and its notification variants.
// Geometry uses a 256-unit canvas, supersampled for smooth transparent edges.
// Run from the repository root: go run scripts/gen-icon.go
package main

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/vector"
)

const (
	n     = 256
	scale = 4
)

func rgb(r, g, b uint8) color.NRGBA { return color.NRGBA{r, g, b, 255} }

// Gradients are generated offline; the app only loads finished PNGs.
func gradient(top, bottom color.NRGBA) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, n*scale, n*scale))
	for y := 0; y < n*scale; y++ {
		for x := 0; x < n*scale; x++ {
			t := (float64(y)*0.75 + float64(x)*0.25) / float64(n*scale-1)
			mix := func(a, b uint8) uint8 { return uint8(float64(a)*(1-t) + float64(b)*t + 0.5) }
			img.SetNRGBA(x, y, rgb(mix(top.R, bottom.R), mix(top.G, bottom.G), mix(top.B, bottom.B)))
		}
	}
	return img
}

type path struct{ *vector.Rasterizer }

func (p path) move(x, y float32) { p.MoveTo(x*scale, y*scale) }
func (p path) line(x, y float32) { p.LineTo(x*scale, y*scale) }
func (p path) curve(x1, y1, x2, y2, x, y float32) {
	p.CubeTo(x1*scale, y1*scale, x2*scale, y2*scale, x*scale, y*scale)
}

func paint(img *image.RGBA, fill image.Image, shape func(path)) {
	p := path{vector.NewRasterizer(n*scale, n*scale)}
	shape(p)
	p.ClosePath()
	p.Draw(img, img.Bounds(), fill, image.Point{})
}

func rounded(x, y, w, h, r float32) func(path) {
	return func(p path) {
		const k = 0.55228475
		p.move(x+r, y)
		p.line(x+w-r, y)
		p.curve(x+w-r+k*r, y, x+w, y+r-k*r, x+w, y+r)
		p.line(x+w, y+h-r)
		p.curve(x+w, y+h-r+k*r, x+w-r+k*r, y+h, x+w-r, y+h)
		p.line(x+r, y+h)
		p.curve(x+r-k*r, y+h, x, y+h-r+k*r, x, y+h-r)
		p.line(x, y+r)
		p.curve(x, y+r-k*r, x+r-k*r, y, x+r, y)
	}
}

func bubble(p path) {
	p.move(74, 57)
	p.line(148, 57)
	p.curve(165, 57, 177, 69, 177, 86)
	p.line(177, 121)
	p.curve(177, 138, 165, 150, 148, 150)
	p.line(106, 150)
	p.line(77, 175)
	p.curve(74, 178, 70, 176, 70, 172)
	p.line(70, 150)
	p.curve(55, 148, 45, 137, 45, 121)
	p.line(45, 86)
	p.curve(45, 69, 57, 57, 74, 57)
}

func render(dot *color.NRGBA) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, n*scale, n*scale))
	// A lit rim keeps the tile distinct on both light and dark panels.
	paint(img, gradient(rgb(108, 181, 249), rgb(26, 58, 138)), rounded(8, 8, 240, 240, 55))
	paint(img, gradient(rgb(43, 126, 224), rgb(29, 53, 127)), rounded(10, 10, 236, 235, 53))
	// The answering bubble is offset below and to the right of the message.
	paint(img, gradient(rgb(167, 239, 248), rgb(73, 187, 222)), func(p path) {
		p.move(123, 105)
		p.line(180, 105)
		p.curve(198, 105, 210, 117, 210, 135)
		p.line(210, 163)
		p.curve(210, 179, 200, 190, 186, 192)
		p.line(186, 208)
		p.curve(186, 212, 182, 214, 179, 211)
		p.line(156, 192)
		p.line(123, 192)
		p.curve(105, 192, 93, 180, 93, 163)
		p.line(93, 135)
		p.curve(93, 117, 105, 105, 123, 105)
	})
	// A narrow separation preserves the overlapping silhouette at tray size.
	paint(img, image.NewUniform(color.NRGBA{15, 49, 109, 85}), func(p path) {
		p.move(92, 102)
		p.line(183, 102)
		p.line(183, 123)
		p.curve(183, 144, 168, 159, 148, 159)
		p.line(107, 159)
		p.line(92, 172)
	})
	paint(img, gradient(rgb(255, 255, 255), rgb(225, 239, 255)), bubble)
	ink := image.NewUniform(rgb(46, 105, 179))
	paint(img, ink, rounded(72, 87, 78, 11, 5.5))
	paint(img, ink, rounded(72, 111, 51, 11, 5.5))
	if dot != nil {
		// Preserve amber unread / red mention semantics and a contrasting ring.
		paint(img, image.NewUniform(rgb(239, 246, 255)), rounded(175, 7, 74, 74, 37))
		paint(img, image.NewUniform(*dot), rounded(182, 14, 60, 60, 30))
	}
	out := image.NewNRGBA(image.Rect(0, 0, n, n))
	xdraw.CatmullRom.Scale(out, out.Bounds(), img, img.Bounds(), draw.Src, nil)
	return out
}

func write(name string, img image.Image) {
	f, err := os.Create("internal/appfiles/icons/" + name + ".png")
	if err != nil {
		panic(err)
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		panic(err)
	}
	if err := f.Close(); err != nil {
		panic(err)
	}
}

func main() {
	amber, red := rgb(245, 166, 35), rgb(230, 68, 86)
	write("icon", render(nil))
	write("icon-unread", render(&amber))
	write("icon-mention", render(&red))
}
