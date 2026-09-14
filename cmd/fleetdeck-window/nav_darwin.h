#ifndef FLEETDECK_NAV_DARWIN_H
#define FLEETDECK_NAV_DARWIN_H

// fleetdeck_observe_navigation sets a navigation delegate on the WKWebView
// that is window's content view, and reports every navigation event to Go
// (fleetdeckNavigationEvent in nav_darwin.go). window must be the pointer
// webview's Window() returns. It returns 0, and sets nothing, when that content
// view is not a WKWebView. webview_go sets no navigation delegate of its own,
// but setting one changes what WebKit does by itself: a page whose web content
// process went away is no longer reloaded, and the window asks for it again
// (navscreen.go).
int fleetdeck_observe_navigation(void *window);

#endif
