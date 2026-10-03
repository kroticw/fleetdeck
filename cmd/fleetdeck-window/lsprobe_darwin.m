//go:build lsprobe

// The AppKit side of the start probe (lsprobe_darwin.go): the launch and
// activation notifications, the first block run once w.Run has started, and
// the quick look at LaunchServices.

#import <AppKit/AppKit.h>
#include "_cgo_export.h"

static BOOL lsprobeActiveSeen;
static BOOL lsprobeActivatedByProbe;

void lsprobeObserve(void) {
	NSNotificationCenter *center = [NSNotificationCenter defaultCenter];
	[center addObserverForName:NSApplicationWillFinishLaunchingNotification object:nil queue:nil usingBlock:^(NSNotification *n) {
		lsprobeNotified("the application will finish launching");
	}];
	[center addObserverForName:NSApplicationDidFinishLaunchingNotification object:nil queue:nil usingBlock:^(NSNotification *n) {
		lsprobeNotified("the application has finished launching");
	}];
	[center addObserverForName:NSApplicationDidBecomeActiveNotification object:nil queue:nil usingBlock:^(NSNotification *n) {
		if (lsprobeActiveSeen) {
			return;
		}
		lsprobeActiveSeen = YES;
		lsprobeNotified(lsprobeActivatedByProbe ? "first activation, made by the probe" : "first activation");
	}];
}

void lsprobeAfterRunStarts(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		lsprobeNotified("the window runs");
		dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 10 * NSEC_PER_SEC), dispatch_get_main_queue(), ^{
			if (lsprobeActiveSeen || [NSApp isActive]) {
				return;
			}
			lsprobeActivatedByProbe = YES;
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
			[NSApp activateIgnoringOtherApps:YES];
#pragma clang diagnostic pop
		});
	});
}

// lsprobeRegistered says whether LaunchServices lists bundle under its own
// bundle identifier: 1 if it does, 0 if not, -1 if the bundle has none. Each
// run of the probe builds its bundle under an identifier of its own, so the
// answer is about this path alone, and it takes milliseconds where a dump
// takes seconds.
int lsprobeRegistered(const char *bundle) {
	@autoreleasepool {
		NSString *path = [[NSString stringWithUTF8String:bundle] stringByResolvingSymlinksInPath];
		NSString *ident = [[NSBundle bundleWithPath:path] bundleIdentifier];
		if (ident == nil) {
			return -1;
		}
		for (NSURL *url in [[NSWorkspace sharedWorkspace] URLsForApplicationsWithBundleIdentifier:ident]) {
			if ([[[url path] stringByResolvingSymlinksInPath] isEqualToString:path]) {
				return 1;
			}
		}
		return 0;
	}
}
