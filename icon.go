package core

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

// TrayIcon renders a small monochrome "⇅" (up/down arrows) glyph as PNG bytes.
// Drawn in black with alpha so macOS can treat it as a template icon that
// adapts to light and dark menu bars.
func TrayIcon() []byte {
	return renderIcon(color.RGBA{0, 0, 0, 255}, nil)
}

// TrayIconColored renders a colored version of the tray icon for Windows.
// Blue arrows on a rounded blue-tinted background, visible in both light and
// dark Windows taskbar themes.
func TrayIconColored() []byte {
	bg := color.RGBA{30, 100, 220, 255}  // blue background
	fg := color.RGBA{255, 255, 255, 255} // white arrows
	return renderIcon(fg, &bg)
}

func renderIcon(fg color.Color, bg *color.RGBA) []byte {
	const s = 32
	img := image.NewRGBA(image.Rect(0, 0, s, s))

	// Fill background if provided (Windows needs a visible background).
	if bg != nil {
		for y := 0; y < s; y++ {
			for x := 0; x < s; x++ {
				// Rounded corners: skip pixels in the 4 corners.
				dx := min(x, s-1-x)
				dy := min(y, s-1-y)
				if dx+dy >= 3 { // simple rounded-rect mask
					img.Set(x, y, *bg)
				}
			}
		}
	}

	set := func(x, y int) {
		if x >= 0 && x < s && y >= 0 && y < s {
			img.Set(x, y, fg)
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
