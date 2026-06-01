//go:build darwin

package core

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation -framework Cocoa
#import <Foundation/Foundation.h>
#import <Cocoa/Cocoa.h>
#include <stdlib.h>

void macNotify(const char *title, const char *body) {
	@autoreleasepool {
		NSString *t = [NSString stringWithUTF8String:title];
		NSString *b = [NSString stringWithUTF8String:body];
		NSString *src = [NSString stringWithFormat:
			@"display notification \"%@\" with title \"%@\" sound name \"Glass\"", b, t];
		NSAppleScript *script = [[NSAppleScript alloc] initWithSource:src];
		[script executeAndReturnError:nil];
	}
}
*/
import "C"
import "unsafe"

// Notify sends a macOS notification from within the SwiftDrop process,
// so the notification centre shows SwiftDrop's icon instead of Script Editor's.
func Notify(title, message string) {
	ct := C.CString(title)
	cm := C.CString(message)
	defer C.free(unsafe.Pointer(ct))
	defer C.free(unsafe.Pointer(cm))
	go C.macNotify(ct, cm)
}
