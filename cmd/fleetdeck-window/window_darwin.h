#ifndef FLEETDECK_WINDOW_DARWIN_H
#define FLEETDECK_WINDOW_DARWIN_H

// The window's appearance: "light", "dark", or anything else for the system's.
void fd_window_set_appearance(const char *choice);
// The appearance the app is drawn in now, by name: "NSAppearanceNameDarkAqua".
const char *fd_window_effective_appearance(void);
// The system's appearance by name, as AppKit draws an app with no appearance of
// its own; "" once the app has one, when AppKit no longer says it.
const char *fd_window_system_appearance(void);

// Opens url in the system's browser.
void fd_open_external(const char *url);

// Makes view the first responder of its window.
void fd_focus_view(void *view);

// The app's own defaults. fd_defaults_double answers found = 0 for a key never set.
double fd_defaults_double(const char *key, int *found);
void fd_defaults_set_double(const char *key, double value);
int fd_defaults_bool(const char *key);
void fd_defaults_set_bool(const char *key, int value);
// fd_defaults_use_suite sends every later default of this process to the suite
// named name instead of the app's own defaults.
void fd_defaults_use_suite(const char *name);

// fd_observe_window calls fleetdeckWindowChanged with "size" when window's
// content changes size, "fullscreen" when it enters or leaves full screen, and
// "glass" when the accessibility display options change.
void fd_observe_window(void *window);
void fd_window_content_size(void *window, double *width, double *height);
int fd_window_is_fullscreen(void *window);
// Puts window into full screen or out of it, as its green button does.
void fd_window_toggle_fullscreen(void *window);

#endif
