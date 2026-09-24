//go:build ignore

// gen-icon draws the app icon (blue rounded square, white speech bubble) so
// the repo needs no binary design assets. Run: go run scripts/gen-icon.go
package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
)

func main() {
	const n = 256
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	blue := color.NRGBA{0x1e, 0x5e, 0xd8, 0xff}
	white := color.NRGBA{0xff, 0xff, 0xff, 0xff}
	inRounded := func(x, y, x0, y0, x1, y1, r int) bool {
		if x < x0 || x >= x1 || y < y0 || y >= y1 {
			return false
		}
		cx, cy := x, y
		if x < x0+r {
			cx = x0 + r
		} else if x >= x1-r {
			cx = x1 - r - 1
		}
		if y < y0+r {
			cy = y0 + r
		} else if y >= y1-r {
			cy = y1 - r - 1
		}
		dx, dy := x-cx, y-cy
		return dx*dx+dy*dy <= r*r
	}
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			switch {
			case inRounded(x, y, 60, 64, 196, 164, 28):
				img.Set(x, y, white)
			case x >= 90 && x < 130 && y >= 160 && y < 200 && (x-90) <= (200-y):
				img.Set(x, y, white) // bubble tail
			case inRounded(x, y, 8, 8, n-8, n-8, 48):
				img.Set(x, y, blue)
			}
		}
	}
	f, err := os.Create("internal/appfiles/icons/icon.png")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}
