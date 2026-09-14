#ifndef FLEETDECK_NAV_DARWIN_H
#define FLEETDECK_NAV_DARWIN_H

// fleetdeck_observe_navigation sets a navigation delegate on the WKWebView
// that is window's content view, and reports every navigation event to Go
// (fleetdeckNavigationEvent in nav_darwin.go). window must be the pointer
// webview's Window() returns. webview_go sets no navigation delegate of its
// own, so this one replaces nothing.
void fleetdeck_observe_navigation(void *window);

#endif
