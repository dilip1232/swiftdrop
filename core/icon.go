package core

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

// TrayIcon renders a small monochrome "⇅" (up/down arrows) glyph as PNG bytes.
// Drawn in black with alpha so macOS can treat it as a template icon that
// adapts to light and dark menu bars. 32×32 is the standard macOS template size.
func TrayIcon() []byte {
	return renderIcon(32, color.RGBA{0, 0, 0, 255}, nil)
}

// TrayIconColored renders a colored 64×64 version of the tray icon for Windows.
// Blue arrows on a rounded blue background, crisp on high-DPI taskbars.
func TrayIconColored() []byte {
	bg := color.RGBA{30, 100, 220, 255}
	fg := color.RGBA{255, 255, 255, 255}
	return renderIcon(64, fg, &bg)
}

// AppIcon renders a 256×256 colored icon suitable for the window title bar and
// taskbar on Windows.
func AppIcon() []byte {
	bg := color.RGBA{30, 100, 220, 255}
	fg := color.RGBA{255, 255, 255, 255}
	return renderIcon(256, fg, &bg)
}

func renderIcon(s int, fg color.Color, bg *color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, s, s))

	// Rounded-corner radius scales with icon size.
	cornerMin := s / 10
	if cornerMin < 3 {
		cornerMin = 3
	}

	if bg != nil {
		for y := 0; y < s; y++ {
			for x := 0; x < s; x++ {
				dx := min(x, s-1-x)
				dy := min(y, s-1-y)
				if dx+dy >= cornerMin {
					img.Set(x, y, *bg)
				}
			}
		}
	}

	// Shaft half-width scales with icon size.
	sw := s / 16
	if sw < 1 {
		sw = 1
	}

	set := func(x, y int) {
		if x >= 0 && x < s && y >= 0 && y < s {
			img.Set(x, y, fg)
		}
	}
	vbar := func(cx, y0, y1 int) {
		for y := y0; y <= y1; y++ {
			for x := cx - sw; x <= cx+sw; x++ {
				set(x, y)
			}
		}
	}
	head := func(cx, apexY, dir, h int) {
		for i := 0; i <= h; i++ {
			y := apexY + dir*i
			for x := cx - i; x <= cx+i; x++ {
				set(x, y)
			}
		}
	}

	// Positions scale proportionally to icon size.
	upX := s * 11 / 32
	vbar(upX, s*8/32, s*26/32)
	head(upX, s*4/32, 1, s*6/32)

	dnX := s * 21 / 32
	vbar(dnX, s*6/32, s*24/32)
	head(dnX, s*28/32, -1, s*6/32)

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
