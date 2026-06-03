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

// macConsentDialog shows a modal NSAlert on the main thread with Accept/Reject
// buttons.  Returns 1 for Accept, 0 for Reject/cancel.
int macConsentDialog(const char *title, const char *body) {
	__block int result = 0;
	dispatch_semaphore_t sem = dispatch_semaphore_create(0);
	dispatch_async(dispatch_get_main_queue(), ^{
		@autoreleasepool {
			NSAlert *alert = [[NSAlert alloc] init];
			[alert setMessageText:[NSString stringWithUTF8String:title]];
			[alert setInformativeText:[NSString stringWithUTF8String:body]];
			[alert setAlertStyle:NSAlertStyleInformational];
			NSButton *accept = [alert addButtonWithTitle:@"Accept"];
			[accept setKeyEquivalent:@"\r"];
			[alert addButtonWithTitle:@"Reject"];
			[NSApp activateIgnoringOtherApps:YES];
			result = ([alert runModal] == NSAlertFirstButtonReturn) ? 1 : 0;
		}
		dispatch_semaphore_signal(sem);
	});
	// Block up to 65 seconds (slightly more than the 60s server timeout).
	dispatch_semaphore_wait(sem, dispatch_time(DISPATCH_TIME_NOW, 65LL * NSEC_PER_SEC));
	return result;
}
*/
import "C"
import "unsafe"

// Notify sends a macOS notification from within the SwiftDrop process,
// so the notification centre shows SwiftDrop's icon instead of Script Editor's.
func Notify(title, message string) {
	go func() {
		ct := C.CString(title)
		cm := C.CString(message)
		C.macNotify(ct, cm)
		C.free(unsafe.Pointer(ct))
		C.free(unsafe.Pointer(cm))
	}()
}

// ConsentDialog shows a native macOS alert with Accept/Reject buttons.
// Blocks until the user responds; returns true for Accept.
func ConsentDialog(title, message string) bool {
	ct := C.CString(title)
	cm := C.CString(message)
	defer C.free(unsafe.Pointer(ct))
	defer C.free(unsafe.Pointer(cm))
	return C.macConsentDialog(ct, cm) == 1
}
