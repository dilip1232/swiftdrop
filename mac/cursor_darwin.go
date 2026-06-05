package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

static void getMouseLocation(double *x, double *y) {
    NSPoint p = [NSEvent mouseLocation];
    *x = p.x;
    *y = p.y;
}
*/
import "C"

// mouseLocation returns the current cursor position in macOS screen
// coordinates (origin at bottom-left of primary screen).
func mouseLocation() (x, y float64) {
	var cx, cy C.double
	C.getMouseLocation(&cx, &cy)
	return float64(cx), float64(cy)
}
