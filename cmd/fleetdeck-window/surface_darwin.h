#ifndef FLEETDECK_SURFACE_DARWIN_H
#define FLEETDECK_SURFACE_DARWIN_H

// fd_surface_create puts a transparent WKWebView for the surface named name
// into container, on the board web view's process pool and data store, with
// scripts[0..count) injected at the start of every document, a message handler
// named fleetdeck, and a navigation delegate that asks Go where each navigation
// goes (fleetdeckSurfaceNavigation).
void *fd_surface_create(void *board, void *container, const char *name, const char **scripts, int count);
void fd_surface_load(void *surface, const char *url);
void fd_surface_eval(void *surface, const char *js);
void fd_surface_focus(void *surface);

// fd_surface_destroy takes the web view down in the order spec 6.8 gives and
// releases everything fd_surface_create allocated.
void fd_surface_destroy(void *surface);

// How many message handler objects are alive, and how many times their class
// was registered: the tests' measure of a surface that leaves nothing behind.
int fd_surface_live_handlers(void);
int fd_surface_handler_class_registrations(void);

// For surface_darwin_test.go, which cannot use cgo itself.
void *fd_test_board_window(double width, double height);
void *fd_test_surface_webview(void *surface);
int fd_test_reports_navigation(void *surface);
int fd_test_draws_background(void *webview);
int fd_test_shares_pool(void *surface, void *board);
int fd_test_shares_store(void *surface, void *board);
int fd_test_user_scripts(void *surface);
int fd_test_subview_count(void *view);

#endif
