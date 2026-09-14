#ifndef FLEETDECK_FRAME_DARWIN_H
#define FLEETDECK_FRAME_DARWIN_H

// A rectangle in the frame's coordinates: points, origin at the window
// content's top left (the frame's views are flipped).
typedef struct {
  double x, y, w, h;
} fd_rect;

// fd_frame_install puts the frame into window: a flipped root view becomes the
// content view, the board -- the web view that was the content view -- its
// first subview, and above it two panels and the capsule row. The panels are
// laid out and given their material by fd_frame_layout and fd_frame_set_mode.
void *fd_frame_install(void *window);

// fd_frame_set_mode wraps the panels in "glass" (NSGlassEffectView, Regular),
// "vibrancy" (NSVisualEffectView, sidebar, within the window) or "opaque" (a
// plain view). The view a surface goes into is kept, and stays over the new
// wrapper with the same frame: over it, not inside, since glass keeps the clicks
// on its own content.
void fd_frame_set_mode(void *frame, const char *mode);

// The panels' edges carry a strip that drags their width, shown only for a
// resizable panel; a drag calls fleetdeckResize with the side, the phase (0
// press, 1 drag, 2 release) and the pointer's x in the window.
void fd_frame_layout(void *frame, fd_rect orchestrator, fd_rect sessions, fd_rect capsules, int orchestratorResizable,
                     int sessionsResizable);

// side 0 is the orchestrator panel, 1 the sessions panel.
void *fd_frame_panel_content(void *frame, int side);
void *fd_frame_capsules(void *frame);
void *fd_frame_board(void *frame);

// fd_frame_board_observed says whether fd_frame_install set the navigation
// delegate on the board (fleetdeck_observe_navigation): 0 when the window's
// content view was not a WKWebView.
int fd_frame_board_observed(void *frame);

int fd_glass_available(void);
int fd_reduce_transparency(void);
int fd_increase_contrast(void);

// For frame_darwin_test.go, which cannot use cgo itself.
void *fd_test_window(double width, double height);
void *fd_test_panel(void *frame, int side);
void *fd_test_root(void *frame);
const char *fd_test_class_name(void *view);
long fd_test_glass_style(void *view);
double fd_test_corner_radius(void *view);
long fd_test_blending_mode(void *view);
long fd_test_material(void *view);
int fd_test_subview_index(void *parent, void *child);
fd_rect fd_test_frame_of(void *view);
fd_rect fd_test_window_frame(void *window);
int fd_test_passes_through(void *view);
void *fd_test_strip(void *frame, int side);
int fd_test_is_hidden(void *view);
void fd_test_mouse(void *view, int phase, double x);
int fd_test_hit_within(void *frame, double x, double y, void *view);
const char *fd_test_hit_chain(void *frame, double x, double y);

#endif
