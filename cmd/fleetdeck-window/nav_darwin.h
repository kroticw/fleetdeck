#ifndef FLEETDECK_NAV_DARWIN_H
#define FLEETDECK_NAV_DARWIN_H

// fleetdeck_observe_navigation sets a navigation delegate on webView, the
// board's WKWebView -- the one webview_go made, which the glass frame keeps
// under its panels (frame_darwin.c) -- and reports every navigation event to Go
// (fleetdeckNavigationEvent in nav_darwin.go). It returns 0, and sets nothing,
// when webView is not a WKWebView. webview_go sets no navigation delegate of
// its own, but setting one changes what WebKit does by itself: a page whose web
// content process went away is no longer reloaded, and the window asks for it
// again (navscreen.go).
int fleetdeck_observe_navigation(void *webView);

// fleetdeck_navigation_reporting adds the same reporting methods to a delegate
// class of the window's own that is not yet registered: the side surfaces'
// (surface_darwin.c), whose delegates also decide where a navigation goes. Each
// delegate reports as the web view it is named for (fleetdeck_set_web_view_name);
// the board's carries no name.
void fleetdeck_navigation_reporting(void *delegateClass);

// fleetdeck_set_web_view_name names object -- a delegate, a message handler --
// for the web view it serves; fleetdeck_web_view_name reads the name back, ""
// for none.
void fleetdeck_set_web_view_name(void *object, const char *name);
const char *fleetdeck_web_view_name(void *object);

#endif
