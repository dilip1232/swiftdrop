//go:build darwin

package core

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation -framework Cocoa -framework UserNotifications
#import <Foundation/Foundation.h>
#import <Cocoa/Cocoa.h>
#import <UserNotifications/UserNotifications.h>
#include <stdlib.h>

// Delegate: show banner+sound even when the app is in the foreground.
@interface SDNotifDelegate : NSObject <UNUserNotificationCenterDelegate>
@end
@implementation SDNotifDelegate
- (void)userNotificationCenter:(UNUserNotificationCenter *)center
       willPresentNotification:(UNNotification *)notification
         withCompletionHandler:(void (^)(UNNotificationPresentationOptions))completionHandler {
	completionHandler(UNNotificationPresentationOptionBanner | UNNotificationPresentationOptionSound);
}
@end

static SDNotifDelegate *_notifDelegate = nil;
static BOOL _authorized = NO;

static void _setupDelegate(void) {
	if (_notifDelegate) return;
	_notifDelegate = [[SDNotifDelegate alloc] init];
	[UNUserNotificationCenter currentNotificationCenter].delegate = _notifDelegate;
}

static void _deliver(NSString *t, NSString *b) {
	UNMutableNotificationContent *content = [[UNMutableNotificationContent alloc] init];
	content.title = t;
	content.body = b;
	content.sound = [UNNotificationSound defaultSound];
	NSString *ident = [NSString stringWithFormat:@"sd-%f", [[NSDate date] timeIntervalSince1970]];
	UNNotificationRequest *req = [UNNotificationRequest requestWithIdentifier:ident content:content trigger:nil];
	[[UNUserNotificationCenter currentNotificationCenter] addNotificationRequest:req withCompletionHandler:nil];
}

void macNotify(const char *title, const char *body) {
	NSString *t = [[NSString alloc] initWithUTF8String:title];
	NSString *b = [[NSString alloc] initWithUTF8String:body];

	dispatch_async(dispatch_get_main_queue(), ^{
		_setupDelegate();
		if (_authorized) {
			_deliver(t, b);
			return;
		}
		UNUserNotificationCenter *center = [UNUserNotificationCenter currentNotificationCenter];
		[center requestAuthorizationWithOptions:(UNAuthorizationOptionAlert | UNAuthorizationOptionSound | UNAuthorizationOptionBadge)
			completionHandler:^(BOOL granted, NSError *error) {
				_authorized = granted;
				if (granted) _deliver(t, b);
			}];
	});
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
