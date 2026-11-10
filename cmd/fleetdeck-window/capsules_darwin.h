#ifndef FLEETDECK_CAPSULES_DARWIN_H
#define FLEETDECK_CAPSULES_DARWIN_H

// fd_bar is the board's model of the bar across the window's top
// (capsulemodel.go), as fd_capsules_draw takes it. Colours are three
// components (0..1), or -1 first for no colour. Strings may be empty: no update
// is an empty updateText, no limits a limitCount of 0.
typedef struct {
  const char *mode;  // "glass", "vibrancy" or "opaque", as the panels'
  const char *brandName, *brandVersion;
  // The fleet shown, what VoiceOver calls its capsule, and the menu under it:
  // every fleet with its waiting count and whether it is the one shown, then
  // All and Add.
  const char *fleetName, *fleetLabel, *fleetAll, *fleetAdd;
  const char **fleetNames;
  const int *fleetWaiting, *fleetCurrent;
  int fleetCount;
  // The limits: their word, a dot per limit in limitRGB, the busiest one's
  // name and number in busyRGB, and every limit for a pointer and VoiceOver.
  const char *limitsLabel, *busyName, *busyText, *limitsTooltip;
  const double *busyRGB, *limitRGB;
  int limitCount;
  const char *updateText, *updateAction;
  // The counters at the right: text in counterRGB on a tint of it, the dot a
  // ring where counterHollow.
  const char **counterTexts;
  const double *counterRGB;
  const int *counterHollow;
  int counterCount;
  // The theme: the group's name, the three segments' (light, system, dark) and
  // the one chosen.
  const char *themeLabel;
  const char *themeLabels[3];
  int themeSegment;
  const double *accentRGB;
  // The window's narrowest for the panels and the board's island under the bar.
  double frameMinWidth;
} fd_bar;

// fd_capsules_draw replaces what is in container -- the frame's capsule row,
// across the bar -- with the bar: from the left the brand, the fleet, the
// limits and the update; from the right the theme, then the counters. A press
// calls fleetdeckCapsulePressed with "fleet:<name>", "fleets", "newFleet",
// "limits", "update" or "theme:<light|system|dark>"; the fleet's capsule opens
// its menu under it.
//
// A row too narrow for every capsule hides the counters. The answer is the
// row's minimum width, its narrowest form; the window's minimum content width
// becomes the wider of frameMinWidth and the row's own start, its minimum and
// the bar's right margin.
double fd_capsules_draw(void *container, const fd_bar *bar);

// fd_capsules_clear takes every capsule out of container: no panel page, no row,
// and no minimum width for the window.
void fd_capsules_clear(void *container);

// For capsules_darwin_test.go, which cannot use cgo itself: what the last draw
// made, and presses sent the way a click sends them. A part is named as
// fd_test_capsule_slot_name names the capsules: "brand", "fleet", "limits",
// "update", "counter" and "theme".
typedef struct {
  double x, w;
  int visible;
} fd_capsule_frame;

int fd_test_capsule_count(void);
int fd_test_capsule_slots(void);
const char *fd_test_capsule_slot_name(int i);
fd_capsule_frame fd_test_capsule_slot_frame(int i);
// The y of a capsule slot's frame and its height, in the row.
double fd_test_capsule_slot_y(int i);
double fd_test_capsule_slot_h(int i);
double fd_test_row_width(void);
double fd_test_min_content_width(void *container);
int fd_test_row_passes_through(void *container);

// Text drawn: "brandName", "brandVersion", "fleetName", "limitsLabel",
// "busyName", "busyText", "limitsTooltip", "updateText", "updateAction",
// "counter<i>", "fleetLabel" (the fleet capsule's VoiceOver label).
const char *fd_test_text(const char *what);
// The green component of a mark's colour: "fleetDot", "busyText", "dot<i>",
// "counter<i>", "counterTint<i>" (the pill's background); -1 for none.
double fd_test_green(const char *what);
// Whether a counter's dot is a ring.
int fd_test_counter_hollow(int i);
// Whether the brand shows the app's icon.
int fd_test_brand_has_icon(void);

// The theme's segments: how many, the one selected, each one's tooltip and
// whether each is an image; a press on segment i.
int fd_test_theme_segments(void);
int fd_test_theme_selected(void);
const char *fd_test_theme_tooltip(int i);
int fd_test_theme_segment_is_image(int i);
long fd_test_segment_border_shape(void);
// Where the selected segment's fill starts in its top row, as a share of its
// height, drawn off screen; and the same for a rounded rectangle in its place,
// -1 without border shapes.
double fd_test_selected_segment_top_inset(void);
double fd_test_rounded_segment_top_inset(void);
void fd_test_press_theme(int i);

// A press on a part's control: "fleet", "limits", "update".
void fd_test_press(const char *what);
// With capture on, the fleet's menu is kept for the tests instead of shown.
void fd_test_capture_menus(int on);
int fd_test_menu_items(void);
const char *fd_test_menu_item_title(int i);
int fd_test_menu_item_state(int i);
int fd_test_menu_item_separator(int i);
// The waiting count an item shows: its badge, or 0.
long fd_test_menu_item_waiting(int i);
void fd_test_menu_pick(int i);
// Where the menu's top left goes, in the window's content from its top left.
double fd_test_menu_x(void);
double fd_test_menu_y(void);
// The fleet capsule's frame in the window's content, from its top left.
fd_capsule_frame fd_test_fleet_in_window(void);
double fd_test_fleet_bottom_in_window(void);

// A click at the middle of a part's control reaches it, hit-tested the way the
// window hit-tests a mouse down; the control is its glass's content.
int fd_test_click_reaches(const char *what);
int fd_test_inside_glass(const char *what);

// The appearance of a capsule's content by slot; "" for none of its own.
const char *fd_test_capsule_slot_appearance(int i);
// The app's own appearance by name, "" for the system's.
const char *fd_test_app_appearance(void);
void fd_test_set_app_appearance(const char *name);
// How light a capsule's own background is, 0 black to 1 white, by slot; -1 for
// a capsule with no background of its own.
double fd_test_capsule_slot_background_brightness(int i);

#endif
