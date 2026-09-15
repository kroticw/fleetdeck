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

// fd_frame_set_drag_band makes the band at the window's top, height points tall
// across the window, drag the window: above the board, below the panels, the
// capsules and the width strips. 0 takes it away.
void fd_frame_set_drag_band(void *frame, double height);

// fd_frame_board_observed says whether fd_frame_install set the navigation
// delegate on the board (fleetdeck_observe_navigation): 0 when the window's
// content view was not a WKWebView.
int fd_frame_board_observed(void *frame);

// fd_frame_titlebar_inset is where the window's zoom button ends, in points
// from the window's left edge: 0 when the window has no zoom button or it is
// hidden.
double fd_frame_titlebar_inset(void *frame);

// fd_frame_titlebar_center is the line the window's buttons are centred on, in
// points from the window's top edge: 0 when the window has no close button or it
// is hidden.
double fd_frame_titlebar_center(void *frame);

int fd_glass_available(void);
int fd_reduce_transparency(void);
int fd_increase_contrast(void);

// For frame_darwin_test.go, which cannot use cgo itself.
void *fd_test_window(double width, double height);
// The layout pass AppKit runs on window before its next frame on screen.
void fd_test_layout_window(void *window);
// A standard window button's frame (kind 0 close, 1 minimize, 2 zoom) from the
// window's top left; all zero when there is none.
fd_rect fd_test_window_button(void *window, int kind);
// The toolbar's item count, -1 with no toolbar; its style; whether the title
// bar is transparent.
long fd_test_toolbar_items(void *window);
long fd_test_toolbar_style(void *window);
int fd_test_titlebar_transparent(void *window);
// Whether a click at (x, y) from the window's top left, routed as the window
// routes one, title bar included, lands on view or inside it.
int fd_test_window_hit_within(void *window, double x, double y, void *view);
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

// The drag band's tests: a window counting drags, zooms, fills and minimizes
// (kind 0 to 3) instead of doing them, and able to say it is in full screen.
void *fd_test_counting_window(double width, double height);
int fd_test_window_calls(int kind);
void fd_test_reset_window_calls(void);
void fd_test_set_full_screen(int on);
// NULL reads the system's setting again.
void fd_test_set_double_click_action(const char *action);
int fd_test_has_fill(void);
void *fd_test_band(void *frame);
void fd_test_press_band(void *frame, long clickCount);
void *fd_test_add_subview(void *parent, fd_rect r);

#endif
