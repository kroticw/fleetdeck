#ifndef FLEETDECK_CAPSULES_DARWIN_H
#define FLEETDECK_CAPSULES_DARWIN_H

// fd_capsules_draw replaces what is in container -- the frame's capsule row --
// with the board's capsules, left to right: the tabs as one segmented control,
// the new card button, then at the right edge the theme button and one capsule
// per limit. mode is "glass", "vibrancy" or "opaque", as the panels'.
//
// limitRGB holds three components (0..1) per limit, or -1 for no colour. A
// press calls fleetdeckCapsulePressed with "tab:<id>", "newCard" or "theme".
//
// A row too narrow for them all shows the limits as one capsule of
// compactText, coloured compactRGB (as limitRGB, one colour) with
// compactTooltip, and then the theme as its icon. The answer is the row's
// minimum width, its narrowest form; the window's minimum content width becomes
// frameMinWidth -- the panels and gaps around the row -- and that.
double fd_capsules_draw(void *container, const char *mode, const char **tabIDs, const char **tabLabels, int tabCount,
                        int selectedTab, const char *newCardLabel, const char *themeLabel, const char **limitLabels,
                        const char **limitTexts, const double *limitValues, const double *limitRGB, int limitCount,
                        const char *compactText, const char *compactTooltip, const double *compactRGB,
                        double frameMinWidth);

// fd_capsules_clear takes every capsule out of container: no panel page, no row,
// and no minimum width for the window.
void fd_capsules_clear(void *container);

// For capsules_darwin_test.go, which cannot use cgo itself: what the last draw
// made, and presses sent the way a click sends them.
typedef struct {
  double x, w;
  int visible;
} fd_capsule_frame;

int fd_test_capsule_count(void);
const char *fd_test_segment_label(int i);
int fd_test_selected_segment(void);
// The tabs' NSControlBorderShape, -1 on a system without it (before macOS 26).
long fd_test_segment_border_shape(void);
// Where the selected segment's fill starts in its top row, as a share of its
// height, drawn off screen; -1 when nothing is drawn.
double fd_test_selected_segment_top_inset(void);
// The same for a rounded rectangle in the tabs' place, drawn beside them; -1 on
// a system without border shapes.
double fd_test_rounded_segment_top_inset(void);
typedef struct {
  double left, right, top, bottom;
} fd_capsule_insets;
fd_capsule_insets fd_test_tabs_insets(void);
const char *fd_test_new_card_title(void);
const char *fd_test_theme_title(void);
double fd_test_level_value(int i);
int fd_test_capsule_slots(void);
const char *fd_test_capsule_slot_name(int i);
fd_capsule_frame fd_test_capsule_slot_frame(int i);
double fd_test_row_width(void);
double fd_test_min_content_width(void *container);
const char *fd_test_compact_text(void);
const char *fd_test_compact_tooltip(void);
const char *fd_test_theme_icon_title(void);
const char *fd_test_theme_icon_tooltip(void);
const char *fd_test_theme_icon_accessibility_label(void);
int fd_test_row_passes_through(void *container);
void fd_test_press_segment(int i);
void fd_test_press_new_card(void);
void fd_test_press_theme_icon(void);
int fd_test_click_reaches_capsule(int which);
int fd_test_capsule_inside_glass(int which);

// The appearance of a capsule's content -- its control or label -- by the
// capsule's order as fd_test_capsule_slot_name has it; "" for none of its own.
const char *fd_test_capsule_slot_appearance(int i);
// The app's own appearance by name, "" for the system's.
const char *fd_test_app_appearance(void);
void fd_test_set_app_appearance(const char *name);
// How light a capsule's own background is, 0 black to 1 white, by the capsule's
// order; -1 for a capsule with no background of its own.
double fd_test_capsule_slot_background_brightness(int i);

#endif
