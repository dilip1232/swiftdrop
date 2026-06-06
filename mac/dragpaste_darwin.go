package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

// getDraggedPaths reads file URLs from the macOS drag pasteboard and returns
// them as a newline-separated C string. Caller must free the result.
static const char* getDraggedPaths() {
    NSPasteboard *pboard = [NSPasteboard pasteboardWithName:NSPasteboardNameDrag];
    NSArray *urls = [pboard readObjectsForClasses:@[[NSURL class]]
                                          options:@{NSPasteboardURLReadingFileURLsOnlyKey: @YES}];
    if (!urls || urls.count == 0) return NULL;

    NSMutableArray *paths = [NSMutableArray arrayWithCapacity:urls.count];
    for (NSURL *url in urls) {
        if (url.filePathURL) [paths addObject:url.path];
    }
    if (paths.count == 0) return NULL;

    NSString *joined = [paths componentsJoinedByString:@"\n"];
    return strdup([joined UTF8String]);
}
*/
import "C"

import (
	"strings"
	"unsafe"
)

// draggedPaths reads file paths from the macOS drag pasteboard.
// Returns nil if the pasteboard is empty or doesn't contain file URLs.
func draggedPaths() []string {
	cstr := C.getDraggedPaths()
	if cstr == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(cstr))
	raw := C.GoString(cstr)
	if raw == "" {
		return nil
	}
	return strings.Split(raw, "\n")
}
