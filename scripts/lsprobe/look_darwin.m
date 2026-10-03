// The quick look at LaunchServices, the same as the probe window's
// (cmd/fleetdeck-window/lsprobe_darwin.m): whether NSWorkspace lists bundle
// under its own bundle identifier. 1 if it does, 0 if not, -1 if the bundle
// has no identifier.

#import <AppKit/AppKit.h>

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
