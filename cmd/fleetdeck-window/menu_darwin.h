#ifndef FLEETDECK_MENU_DARWIN_H
#define FLEETDECK_MENU_DARWIN_H

// fleetdeck_install_menu builds the app's main menu bar -- an App menu with
// Quit, and an Edit menu with the standard Undo/Redo/Cut/Copy/Paste/Select
// All set -- and installs it on the already-running NSApplication. Safe to
// call any time after webview.New() returns: by then the app has already
// finished launching (see main.go).
void fleetdeck_install_menu(void);

// fleetdeck_install_close_to_hide replaces window's delegate so the close
// button hides the window instead of destroying it, and NSApp's delegate so
// a Dock icon click brings the same window back. window must be the
// pointer webview's Window() returns.
void fleetdeck_install_close_to_hide(void *window);

// fleetdeck_window_should_close_for_test drives the installed window
// delegate's windowShouldClose: exactly as AppKit would, without any user
// interaction -- so a headless test can assert the delegate actually hides
// the window and refuses the close, not just that it was attached.
int fleetdeck_window_should_close_for_test(void *window);

#endif
