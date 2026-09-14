#ifndef FLEETDECK_CAPSULES_DARWIN_H
#define FLEETDECK_CAPSULES_DARWIN_H

// fd_capsules_draw replaces what is in container -- the frame's capsule row --
// with the board's capsules, left to right: the tabs as one segmented control,
// the new card button, then at the right edge the theme button and one capsule
// per limit. mode is "glass", "vibrancy" or "opaque", as the panels'.
//
// limitRGB holds three components (0..1) per limit, or -1 for no colour. A
// press calls fleetdeckCapsulePressed with "tab:<id>", "newCard" or "theme".
void fd_capsules_draw(void *container, const char *mode, const char **tabIDs, const char **tabLabels, int tabCount,
                      int selectedTab, const char *newCardLabel, const char *themeLabel, const char **limitLabels,
                      const char **limitTexts, const double *limitValues, const double *limitRGB, int limitCount);

// fd_capsules_clear takes every capsule out of container: no panel page, no row.
void fd_capsules_clear(void *container);

// For capsules_darwin_test.go, which cannot use cgo itself: what the last draw
// made, and presses sent the way a click sends them.
int fd_test_capsule_count(void);
const char *fd_test_segment_label(int i);
int fd_test_selected_segment(void);
const char *fd_test_new_card_title(void);
const char *fd_test_theme_title(void);
double fd_test_level_value(int i);
int fd_test_row_passes_through(void *container);
void fd_test_press_segment(int i);
void fd_test_press_new_card(void);
int fd_test_click_reaches_capsule(int which);

#endif
