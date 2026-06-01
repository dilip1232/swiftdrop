package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

// trayIcon renders a small monochrome "⇅" (up/down arrows) glyph as PNG bytes.
// Drawn in black with alpha so macOS can treat it as a template icon that
// adapts to light and dark menu bars.
func trayIcon() []byte {
	const s = 32
	img := image.NewRGBA(image.Rect(0, 0, s, s))
	black := color.RGBA{0, 0, 0, 255}

	set := func(x, y int) {
		if x >= 0 && x < s && y >= 0 && y < s {
			img.Set(x, y, black)
		}
	}
	vbar := func(cx, y0, y1 int) {
		for y := y0; y <= y1; y++ {
			for x := cx - 1; x <= cx+1; x++ {
				set(x, y)
			}
		}
	}
	// Filled triangle arrowhead pointing up (apex at top) or down.
	head := func(cx, apexY, dir, h int) {
		for i := 0; i <= h; i++ {
			y := apexY + dir*i
			for x := cx - i; x <= cx+i; x++ {
				set(x, y)
			}
		}
	}

	// Up arrow (left).
	upX := 11
	vbar(upX, 8, 26)
	head(upX, 4, 1, 6)
	// Down arrow (right).
	dnX := 21
	vbar(dnX, 6, 24)
	head(dnX, 28, -1, 6)

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
