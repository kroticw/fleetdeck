// The capsule row: tabs, new card, theme and limits as system controls in
// glass, drawn from the board's model (capsulemodel.go). Raw runtime calls, as
// frame_darwin.c.
//
// The row is one NSGlassEffectContainerView, the size of the row, so the glass
// of neighbouring capsules merges (spec 5.1). It passes a click on to the board
// wherever no capsule is under the pointer: the gaps between capsules are over
// the board.
//
// A row too narrow for every capsule compacts, and never lets two capsules
// overlap: first the limits become one capsule of text, then the theme becomes
// its icon. The tabs and the new card button are always whole. The row's
// narrowest form is its minimum, which the window keeps room for.
#include "capsules_darwin.h"

#include "_cgo_export.h"

#include <CoreGraphics/CGColor.h>
#include <CoreGraphics/CGGeometry.h>
#include <objc/message.h>
#include <objc/objc.h>
#include <objc/runtime.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

extern void *objc_autoreleasePoolPush(void);
extern void objc_autoreleasePoolPop(void *pool);

static id cls(const char *name) { return (id)objc_getClass(name); }
static SEL sel(const char *name) { return sel_registerName(name); }

static id send0(id recv, SEL s) { return ((id (*)(id, SEL))objc_msgSend)(recv, s); }
static id send1(id recv, SEL s, id a) { return ((id (*)(id, SEL, id))objc_msgSend)(recv, s, a); }
static void sendVoid0(id recv, SEL s) { ((void (*)(id, SEL))objc_msgSend)(recv, s); }
static void sendVoid1(id recv, SEL s, id a) { ((void (*)(id, SEL, id))objc_msgSend)(recv, s, a); }
static void sendVoidLong(id recv, SEL s, long v) { ((void (*)(id, SEL, long))objc_msgSend)(recv, s, v); }
static void sendVoidDouble(id recv, SEL s, double v) { ((void (*)(id, SEL, double))objc_msgSend)(recv, s, v); }
static void sendVoidBool(id recv, SEL s, signed char v) { ((void (*)(id, SEL, signed char))objc_msgSend)(recv, s, v); }
static void sendVoidRect(id recv, SEL s, CGRect r) { ((void (*)(id, SEL, CGRect))objc_msgSend)(recv, s, r); }
static void sendVoidPoint(id recv, SEL s, CGPoint p) { ((void (*)(id, SEL, CGPoint))objc_msgSend)(recv, s, p); }
static void sendVoidSize(id recv, SEL s, CGSize v) { ((void (*)(id, SEL, CGSize))objc_msgSend)(recv, s, v); }
static long sendLong0(id recv, SEL s) { return ((long (*)(id, SEL))objc_msgSend)(recv, s); }
static double sendDouble0(id recv, SEL s) { return ((double (*)(id, SEL))objc_msgSend)(recv, s); }
static CGSize sendSize0(id recv, SEL s) { return ((CGSize (*)(id, SEL))objc_msgSend)(recv, s); }

static CGRect sendRect0(id recv, SEL s) {
#if defined(__x86_64__)
  CGRect r;
  ((void (*)(CGRect *, id, SEL))objc_msgSend_stret)(&r, recv, s);
  return r;
#else
  return ((CGRect (*)(id, SEL))objc_msgSend)(recv, s);
#endif
}

static CGSize fittingSize(id view) { return sendSize0(view, sel("fittingSize")); }

static id nsstring(const char *s) {
  return ((id (*)(id, SEL, const char *))objc_msgSend)(cls("NSString"), sel("stringWithUTF8String:"), s ? s : "");
}

static const char *cstring(id s) {
  return s ? ((const char *(*)(id, SEL))objc_msgSend)(s, sel("UTF8String")) : "";
}

static id initWithFrame(id klass, CGRect r) {
  return ((id (*)(id, SEL, CGRect))objc_msgSend)(send0(klass, sel("alloc")), sel("initWithFrame:"), r);
}

// Added to parent, which then owns it.
static void adopt(id parent, id child) {
  sendVoid1(parent, sel("addSubview:"), child);
  sendVoid0(child, sel("release"));
}

static int respondsTo(id object, const char *selector) {
  return ((signed char (*)(id, SEL, SEL))objc_msgSend)(object, sel("respondsToSelector:"), sel(selector)) != 0;
}

// --- what the last draw made -----------------------------------------------------

enum { tagTabs = 1, tagNewCard = 2, tagTheme = 3 };

#define maxTabs 8
#define maxLimits 8

// The tabs' ids for a press; the controls and their capsules, for placing them
// as the row's width changes and for the tests to read. Every capsule is the
// row's subview, so a cleared row takes them all and they are forgotten here.
static char *tabIDsDrawn[maxTabs];
static int tabsDrawn;
static id rowDrawn;
static id segmentedDrawn, newCardDrawn, themeDrawn, themeIconDrawn, compactLabelDrawn;
static id levelsDrawn[maxLimits];
static id tabsCapsule, newCardCapsule, compactCapsule, themeCapsule, themeIconCapsule;
static id limitCapsules[maxLimits];
static int limitsDrawn;
// capsulesDrawn: how many capsules the row shows.
static int capsulesDrawn;

static void placeCapsules(double rowWidth);
static id appAppearance(void);

// --- the row: a container that lets clicks through its gaps ---------------------

static Class rowSuper;

static id rowHitTest(id self, SEL _cmd, CGPoint point) {
  struct objc_super up = {self, rowSuper};
  id hit = ((id (*)(struct objc_super *, SEL, CGPoint))objc_msgSendSuper)(&up, _cmd, point);
  if (hit == self) return (id)0;
  if (respondsTo(self, "contentView") && hit == send0(self, sel("contentView"))) return (id)0;
  return hit;
}

// A window resized lays the frame out again without drawing the capsules again:
// the row follows its width here.
//
// The capsules are placed only here, and none follows the row by autoresizing:
// the glass container sizes its content view later, in the window's layout
// pass. In v0.10.1 the theme and the limits kept to the row's right edge by
// autoresizing: laid out wider on entering full screen, they were placed for
// the new width here, then moved by the difference again when the content view
// caught up, and lay over the sessions panel.
static void rowSetFrameSize(id self, SEL _cmd, CGSize size) {
  struct objc_super up = {self, rowSuper};
  ((void (*)(struct objc_super *, SEL, CGSize))objc_msgSendSuper)(&up, _cmd, size);
  if (self == rowDrawn) placeCapsules(size.width);
}

static Class rowClass(void) {
  static Class klass;
  if (klass) return klass;
  rowSuper = (Class)objc_getClass("NSGlassEffectContainerView");
  if (!rowSuper) rowSuper = (Class)objc_getClass("NSView");
  klass = objc_allocateClassPair(rowSuper, "FleetdeckCapsuleRow", 0);
  class_addMethod(klass, sel("hitTest:"), (IMP)rowHitTest, "@@:{CGPoint=dd}");
  class_addMethod(klass, sel("setFrameSize:"), (IMP)rowSetFrameSize, "v@:{CGSize=dd}");
  objc_registerClassPair(klass);
  return klass;
}

// --- presses -------------------------------------------------------------------

static void capsulePressed(id self, SEL _cmd, id sender) {
  (void)self;
  (void)_cmd;
  switch (sendLong0(sender, sel("tag"))) {
    case tagTabs: {
      long i = sendLong0(sender, sel("selectedSegment"));
      if (i < 0 || i >= tabsDrawn) return;
      char action[64];
      strcpy(action, "tab:");
      strncat(action, tabIDsDrawn[i], sizeof action - strlen(action) - 1);
      fleetdeckCapsulePressed(action);
      return;
    }
    case tagNewCard:
      fleetdeckCapsulePressed((char *)"newCard");
      return;
    case tagTheme:
      fleetdeckCapsulePressed((char *)"theme");
      return;
  }
}

static id target(void) {
  static id instance;
  if (instance) return instance;
  Class klass = objc_allocateClassPair((Class)objc_getClass("NSObject"), "FleetdeckCapsuleTarget", 0);
  class_addMethod(klass, sel("pressed:"), (IMP)capsulePressed, "v@:@");
  objc_registerClassPair(klass);
  instance = send0((id)klass, sel("new"));
  return instance;
}

// --- capsules ------------------------------------------------------------------

static const double capsuleHeight = 32, capsulePadding = 12, capsuleGap = 8;

// A capsule of the row's material around content, width fitted to it with
// padding on either side. The row places it (placeCapsules); it never moves by
// autoresizing.
static id capsule(const char *mode, id content, double x, double padding) {
  CGSize fit = fittingSize(content);
  CGRect frame = CGRectMake(x, 0, fit.width + 2 * padding, capsuleHeight);
  CGRect inner = CGRectMake(padding, (capsuleHeight - fit.height) / 2, fit.width, fit.height);
  id wrapper;
  id holder = initWithFrame(cls("NSView"), CGRectMake(0, 0, frame.size.width, frame.size.height));
  sendVoidRect(content, sel("setFrame:"), inner);
  sendVoid1(holder, sel("addSubview:"), content);
  if (strcmp(mode, "glass") == 0 && cls("NSGlassEffectView")) {
    // The control is the glass's content: only NSGlassEffectView's contentView
    // is drawn inside the glass. In v0.10.0 the control lay beside the glass in
    // a plain view, and the row's NSGlassEffectContainerView, which draws its
    // glass in a view above its own content, drew the glass over the labels.
    // The reason for moving it out was a hit test on a window never laid out,
    // where the content view had no size yet; laid out, a click reaches it.
    wrapper = initWithFrame(cls("NSGlassEffectView"), frame);
    sendVoidLong(wrapper, sel("setStyle:"), 0);
    sendVoidDouble(wrapper, sel("setCornerRadius:"), capsuleHeight / 2);
    sendVoidLong(holder, sel("setAutoresizingMask:"), 18);
    sendVoid1(wrapper, sel("setContentView:"), holder);
  } else {
    if (strcmp(mode, "opaque") == 0) {
      wrapper = initWithFrame(cls("NSView"), frame);
    } else {
      wrapper = initWithFrame(cls("NSVisualEffectView"), frame);
      sendVoidLong(wrapper, sel("setBlendingMode:"), 1);  // within the window
      sendVoidLong(wrapper, sel("setMaterial:"), 6);      // popover
      sendVoidLong(wrapper, sel("setState:"), 1);
    }
    sendVoidBool(wrapper, sel("setWantsLayer:"), 1);
    id layer = send0(wrapper, sel("layer"));
    sendVoidDouble(layer, sel("setCornerRadius:"), capsuleHeight / 2);
    if (strcmp(mode, "opaque") == 0) {
      // Resolved in the app's appearance, as the capsule's controls are drawn:
      // a CGColor taken outside it is resolved in the system's, and was white
      // under white text with the app dark and the system light (runs
      // 34863293838 and 34864709919).
      void (^resolve)(void) = ^{
        id colour = send0(cls("NSColor"), sel("controlBackgroundColor"));
        ((void (*)(id, SEL, void *))objc_msgSend)(layer, sel("setBackgroundColor:"),
                                                  ((void *(*)(id, SEL))objc_msgSend)(colour, sel("CGColor")));
      };
      id appearance = appAppearance();
      if (appearance && respondsTo(appearance, "performAsCurrentDrawingAppearance:")) {
        ((void (*)(id, SEL, void (^)(void)))objc_msgSend)(appearance, sel("performAsCurrentDrawingAppearance:"), resolve);
      } else {
        resolve();
      }
    }
    sendVoid1(wrapper, sel("addSubview:"), holder);
  }
  sendVoid0(holder, sel("release"));
  return wrapper;
}

static id button(const char *title, long tag) {
  id b = ((id (*)(id, SEL, id, id, SEL))objc_msgSend)(cls("NSButton"), sel("buttonWithTitle:target:action:"),
                                                      nsstring(title), target(), sel("pressed:"));
  sendVoidBool(b, sel("setBordered:"), 0);
  sendVoidLong(b, sel("setTag:"), tag);
  return b;
}

static id label(const char *text) {
  return send1(cls("NSTextField"), sel("labelWithString:"), nsstring(text));
}

static id row(id views[], int count) {
  id array = ((id (*)(id, SEL, id *, unsigned long))objc_msgSend)(cls("NSArray"), sel("arrayWithObjects:count:"), views,
                                                                  (unsigned long)count);
  id stack = send1(cls("NSStackView"), sel("stackViewWithViews:"), array);
  sendVoidLong(stack, sel("setOrientation:"), 0);  // horizontal
  sendVoidDouble(stack, sel("setSpacing:"), 6);
  return stack;
}

// rgb is three components (0..1), or -1 first for no colour.
static id srgb(const double *rgb) {
  if (rgb[0] < 0) return (id)0;
  return ((id (*)(id, SEL, double, double, double, double))objc_msgSend)(
      cls("NSColor"), sel("colorWithSRGBRed:green:blue:alpha:"), rgb[0], rgb[1], rgb[2], 1.0);
}

static double widthOf(id capsuleView) { return capsuleView ? sendRect0(capsuleView, sel("frame")).size.width : 0; }

static void put(id capsuleView, double x, int shown) {
  if (!capsuleView) return;
  sendVoidPoint(capsuleView, sel("setFrameOrigin:"), CGPointMake(x, 0));
  sendVoidBool(capsuleView, sel("setHidden:"), shown ? 0 : 1);
  if (shown) capsulesDrawn++;
}

// The tabs and the new card button, as wide as they always are.
static double leftWidth(void) { return widthOf(tabsCapsule) + capsuleGap + widthOf(newCardCapsule); }

// The compact limits and their gap to the theme, or nothing with no limits.
static double compactWidth(void) { return limitsDrawn > 0 ? widthOf(compactCapsule) + capsuleGap : 0; }

// placeCapsules lays the row out in rowWidth: the tabs and the new card button
// from the left edge, the limits and the theme from the right, in the widest of
// three forms that leaves a gap between the two groups -- every limit and the
// theme's label; the compact limits and the theme's label; the compact limits
// and the theme's icon. The last is the row's minimum, and is what is left when
// even it does not fit.
static void placeCapsules(double rowWidth) {
  if (!rowDrawn) return;
  // The row's width is the frame's arithmetic in floating point: a row laid out
  // at exactly a form's width still fits it.
  const double slack = 0.01;
  double full = widthOf(themeCapsule);
  for (int i = 0; i < limitsDrawn; i++) full += widthOf(limitCapsules[i]) + capsuleGap;
  int stage = 2;
  if (leftWidth() + capsuleGap + full <= rowWidth + slack) {
    stage = 0;
  } else if (leftWidth() + capsuleGap + compactWidth() + widthOf(themeCapsule) <= rowWidth + slack) {
    stage = 1;
  }
  capsulesDrawn = 0;
  put(tabsCapsule, 0, 1);
  put(newCardCapsule, widthOf(tabsCapsule) + capsuleGap, 1);

  // From the right edge leftwards: the last limit first, the theme last.
  double right = rowWidth;
  for (int i = limitsDrawn - 1; i >= 0; i--) {
    if (stage == 0) {
      right -= widthOf(limitCapsules[i]);
      put(limitCapsules[i], right, 1);
      right -= capsuleGap;
    } else {
      put(limitCapsules[i], 0, 0);
    }
  }
  if (stage > 0 && compactCapsule) {
    right -= widthOf(compactCapsule);
    put(compactCapsule, right, 1);
    right -= capsuleGap;
  } else {
    put(compactCapsule, 0, 0);
  }
  id theme = stage == 2 ? themeIconCapsule : themeCapsule;
  put(stage == 2 ? themeCapsule : themeIconCapsule, 0, 0);
  put(theme, right - widthOf(theme), 1);
}

// --- the app's appearance -------------------------------------------------------

// Every capsule takes the theme chosen in fleetdeck, whatever the system's: its
// controls inherit the app's appearance, and an opaque capsule's background is
// resolved in it. On the macOS 26 stand with real glass (run 34868250061) a
// capsule's glass took its tint from the board under it, so controls drawn in
// the system's mode were dark on dark glass or light on light.
static id appAppearance(void) {
  return send0(send0(cls("NSApplication"), sel("sharedApplication")), sel("effectiveAppearance"));
}

static const char *appearanceName(id appearance) {
  return appearance ? cstring(send0(appearance, sel("name"))) : "";
}

static void setMinContentWidth(id window, double width) {
  if (!window) return;
  CGSize min = sendSize0(window, sel("contentMinSize"));
  sendVoidSize(window, sel("setContentMinSize:"), CGSizeMake(width, min.height));
}

// A window narrower than its minimum grows to it, as one a person drags is
// stopped there: at once, its left edge where it is, no wider than its screen's
// visible frame. A screen narrower than the minimum leaves the row overlapping.
static void growToMinContentWidth(id window, double width) {
  if (!window) return;
  if ((unsigned long)sendLong0(window, sel("styleMask")) & (1UL << 14)) return;  // full screen: the screen's width
  double content = sendRect0(send0(window, sel("contentView")), sel("bounds")).size.width;
  if (content >= width) return;
  CGRect frame = sendRect0(window, sel("frame"));
  double wider = frame.size.width + width - content;
  id screen = send0(window, sel("screen"));
  if (!screen) screen = send0(cls("NSScreen"), sel("mainScreen"));
  if (screen) {
    double visible = sendRect0(screen, sel("visibleFrame")).size.width;
    if (wider > visible) wider = visible;
  }
  if (wider <= frame.size.width) return;
  frame.size.width = wider;
  ((void (*)(id, SEL, CGRect, signed char))objc_msgSend)(window, sel("setFrame:display:"), frame, 1);
}

void fd_capsules_clear(void *container) {
  void *pool = objc_autoreleasePoolPush();
  id old = send0(send0((id)container, sel("subviews")), sel("copy"));
  for (long i = 0, n = sendLong0(old, sel("count")); i < n; i++) {
    sendVoid0(((id (*)(id, SEL, unsigned long))objc_msgSend)(old, sel("objectAtIndex:"), (unsigned long)i),
              sel("removeFromSuperview"));
  }
  sendVoid0(old, sel("release"));
  rowDrawn = (id)0;
  segmentedDrawn = newCardDrawn = themeDrawn = themeIconDrawn = compactLabelDrawn = (id)0;
  tabsCapsule = newCardCapsule = compactCapsule = themeCapsule = themeIconCapsule = (id)0;
  for (int i = 0; i < maxLimits; i++) limitCapsules[i] = levelsDrawn[i] = (id)0;
  limitsDrawn = 0;
  capsulesDrawn = 0;
  // No row, nothing for the window to keep room for.
  setMinContentWidth(send0((id)container, sel("window")), 0);
  objc_autoreleasePoolPop(pool);
}

double fd_capsules_draw(void *container, const char *mode, const char **tabIDs, const char **tabLabels, int tabCount,
                        int selectedTab, const char *newCardLabel, const char *themeLabel, const char **limitLabels,
                        const char **limitTexts, const double *limitValues, const double *limitRGB, int limitCount,
                        const char *compactText, const char *compactTooltip, const double *compactRGB,
                        double frameMinWidth) {
  void *pool = objc_autoreleasePoolPush();
  id parent = (id)container;
  fd_capsules_clear(container);
  // The window's log says the capsules' material and the appearance they are
  // drawn in, once each time either changes: glass, vibrancy and opaque take
  // their colours from different places, and a stand's screenshot cannot tell
  // them apart. A stand checks this wiring by it; whether the capsules can be
  // read is for its screenshot.
  static char said[96];
  char now[96];
  snprintf(now, sizeof now, "%s, in %s", mode, appearanceName(appAppearance()));
  if (strcmp(now, said) != 0) {
    strncpy(said, now, sizeof said - 1);
    fprintf(stderr, "fleetdeck-window: the capsules are drawn in %s\n", now);
  }

  CGRect bounds = sendRect0(parent, sel("bounds"));
  id rowView = initWithFrame((id)rowClass(), bounds);
  sendVoidLong(rowView, sel("setAutoresizingMask:"), 18);
  id into = rowView;
  if (respondsTo(rowView, "setContentView:")) {
    into = initWithFrame(cls("NSView"), CGRectMake(0, 0, bounds.size.width, bounds.size.height));
    sendVoidLong(into, sel("setAutoresizingMask:"), 18);
    sendVoid1(rowView, sel("setContentView:"), into);
    sendVoid0(into, sel("release"));
  }
  adopt(parent, rowView);

  for (int i = 0; i < tabsDrawn; i++) free(tabIDsDrawn[i]);
  tabsDrawn = tabCount < maxTabs ? tabCount : maxTabs;
  id labels = send0(cls("NSMutableArray"), sel("array"));
  for (int i = 0; i < tabsDrawn; i++) {
    tabIDsDrawn[i] = strdup(tabIDs[i]);
    sendVoid1(labels, sel("addObject:"), nsstring(tabLabels[i]));
  }

  segmentedDrawn = ((id (*)(id, SEL, id, long, id, SEL))objc_msgSend)(
      cls("NSSegmentedControl"), sel("segmentedControlWithLabels:trackingMode:target:action:"), labels,
      0 /* select one */, target(), sel("pressed:"));
  sendVoidLong(segmentedDrawn, sel("setTag:"), tagTabs);
  sendVoidLong(segmentedDrawn, sel("setSelectedSegment:"), selectedTab);
  // A capsule in the capsule, from macOS 26: left automatic, the control and its
  // selected segment are rounded rectangles at this size (NSControlBorderShape).
  // With the same room on every side the two capsules are concentric.
  double tabsPadding = capsulePadding;
  if (respondsTo(segmentedDrawn, "setBorderShape:")) {
    sendVoidLong(segmentedDrawn, sel("setBorderShape:"), 1);  // NSControlBorderShapeCapsule
    tabsPadding = (capsuleHeight - fittingSize(segmentedDrawn).height) / 2;
  }
  tabsCapsule = capsule(mode, segmentedDrawn, 0, tabsPadding);
  adopt(into, tabsCapsule);

  newCardDrawn = button(newCardLabel, tagNewCard);
  newCardCapsule = capsule(mode, newCardDrawn, 0, capsulePadding);
  adopt(into, newCardCapsule);

  limitsDrawn = limitCount < maxLimits ? limitCount : maxLimits;
  for (int i = 0; i < limitsDrawn; i++) {
    id level = initWithFrame(cls("NSLevelIndicator"), CGRectMake(0, 0, 44, 8));
    sendVoidLong(level, sel("setLevelIndicatorStyle:"), 1);  // continuous capacity
    sendVoidDouble(level, sel("setMinValue:"), 0);
    sendVoidDouble(level, sel("setMaxValue:"), 100);
    sendVoidDouble(level, sel("setDoubleValue:"), limitValues[i]);
    id colour = srgb(&limitRGB[3 * i]);
    if (colour && respondsTo(level, "setFillColor:")) sendVoid1(level, sel("setFillColor:"), colour);
    levelsDrawn[i] = level;
    id views[3] = {label(limitLabels[i]), level, label(limitTexts[i])};
    id content = row(views, 3);
    sendVoid0(level, sel("release"));
    limitCapsules[i] = capsule(mode, content, 0, capsulePadding);
    adopt(into, limitCapsules[i]);
  }

  // The limits as one line of text, for a row with no room for the indicators.
  // The window has no other place for the limits, so this capsule is never
  // taken away.
  if (limitsDrawn > 0) {
    compactLabelDrawn = label(compactText);
    id colour = srgb(compactRGB);
    if (colour) sendVoid1(compactLabelDrawn, sel("setTextColor:"), colour);
    sendVoid1(compactLabelDrawn, sel("setToolTip:"), nsstring(compactTooltip));
    compactCapsule = capsule(mode, compactLabelDrawn, 0, capsulePadding);
    adopt(into, compactCapsule);
  }

  themeDrawn = button(themeLabel, tagTheme);
  themeCapsule = capsule(mode, themeDrawn, 0, capsulePadding);
  adopt(into, themeCapsule);

  // The theme with no room for its label: the same press, and the label -- the
  // mode it is in -- is what a pointer or VoiceOver finds on it.
  themeIconDrawn = button("◐", tagTheme);
  sendVoid1(themeIconDrawn, sel("setToolTip:"), nsstring(themeLabel));
  sendVoid1(themeIconDrawn, sel("setAccessibilityLabel:"), nsstring(themeLabel));
  themeIconCapsule = capsule(mode, themeIconDrawn, 0, capsulePadding);
  adopt(into, themeIconCapsule);

  rowDrawn = rowView;
  placeCapsules(bounds.size.width);

  double rowMin = leftWidth() + capsuleGap + compactWidth() + widthOf(themeIconCapsule);
  setMinContentWidth(send0(parent, sel("window")), frameMinWidth + rowMin);
  growToMinContentWidth(send0(parent, sel("window")), frameMinWidth + rowMin);

  objc_autoreleasePoolPop(pool);
  return rowMin;
}

// --- for the tests ---------------------------------------------------------------

int fd_test_capsule_count(void) { return capsulesDrawn; }

const char *fd_test_segment_label(int i) {
  return cstring(((id (*)(id, SEL, long))objc_msgSend)(segmentedDrawn, sel("labelForSegment:"), i));
}

int fd_test_selected_segment(void) { return (int)sendLong0(segmentedDrawn, sel("selectedSegment")); }

long fd_test_segment_border_shape(void) {
  if (!segmentedDrawn || !respondsTo(segmentedDrawn, "borderShape")) return -1;
  return sendLong0(segmentedDrawn, sel("borderShape"));
}

// The tabs drawn into a bitmap of their own -- the control alone, not the
// screen -- and where the drawn fill starts in its top row, as a share of the
// fill's height. Drawn off screen, the control's track has nothing under it and
// only the selected segment's fill is left: a capsule's top row starts about
// half its height in, a rounded rectangle's a fifth. -1 when nothing is drawn.
static double topInsetOf(id control);

double fd_test_selected_segment_top_inset(void) { return segmentedDrawn ? topInsetOf(segmentedDrawn) : -1; }

// The same measure of a stand-in for the tabs: the same labels, selection, size
// and place, as a rounded rectangle. On a 1x screen the drawn capsule's top row
// starts nearer its edge than on a 2x one (0.375 against 0.46), so the tabs are
// held to this reference drawn beside them, not to a number. -1 on a system
// without border shapes.
double fd_test_rounded_segment_top_inset(void) {
  if (!segmentedDrawn || !respondsTo(segmentedDrawn, "setBorderShape:")) return -1;
  void *pool = objc_autoreleasePoolPush();
  long count = sendLong0(segmentedDrawn, sel("segmentCount"));
  id labels = send0(cls("NSMutableArray"), sel("array"));
  for (long i = 0; i < count; i++) {
    sendVoid1(labels, sel("addObject:"), ((id (*)(id, SEL, long))objc_msgSend)(segmentedDrawn, sel("labelForSegment:"), i));
  }
  id reference = ((id (*)(id, SEL, id, long, id, SEL))objc_msgSend)(
      cls("NSSegmentedControl"), sel("segmentedControlWithLabels:trackingMode:target:action:"), labels, 0, (id)0, (SEL)0);
  sendVoidLong(reference, sel("setSelectedSegment:"), sendLong0(segmentedDrawn, sel("selectedSegment")));
  sendVoidLong(reference, sel("setBorderShape:"), 2);  // NSControlBorderShapeRoundedRectangle
  sendVoidRect(reference, sel("setFrame:"), sendRect0(segmentedDrawn, sel("frame")));
  sendVoid1(send0(segmentedDrawn, sel("superview")), sel("addSubview:"), reference);
  double out = topInsetOf(reference);
  sendVoid0(reference, sel("removeFromSuperview"));
  objc_autoreleasePoolPop(pool);
  return out;
}

static double topInsetOf(id control) {
  void *pool = objc_autoreleasePoolPush();
  CGRect b = sendRect0(control, sel("bounds"));
  id rep = ((id (*)(id, SEL, CGRect))objc_msgSend)(control, sel("bitmapImageRepForCachingDisplayInRect:"), b);
  double out = -1;
  if (rep) {
    ((void (*)(id, SEL, CGRect, id))objc_msgSend)(control, sel("cacheDisplayInRect:toBitmapImageRep:"), b, rep);
    long w = sendLong0(rep, sel("pixelsWide")), h = sendLong0(rep, sel("pixelsHigh"));
    long top = -1, bottom = -1, left = -1;
    for (long y = 0; y < h; y++) {
      for (long x = 0; x < w; x++) {
        id c = ((id (*)(id, SEL, long, long))objc_msgSend)(rep, sel("colorAtX:y:"), x, y);
        if (c && sendDouble0(c, sel("alphaComponent")) > 0.08) {
          if (top < 0) top = y, left = x;
          bottom = y;
          break;
        }
      }
    }
    if (top >= 0 && bottom > top) out = (double)left / (double)(bottom - top + 1);
  }
  objc_autoreleasePoolPop(pool);
  return out;
}

// The room between the tabs and the edges of their capsule, on each side.
fd_capsule_insets fd_test_tabs_insets(void) {
  fd_capsule_insets out = {-1, -1, -1, -1};
  if (!segmentedDrawn || !tabsCapsule) return out;
  CGRect inner = sendRect0(segmentedDrawn, sel("frame"));
  CGRect outer = sendRect0(tabsCapsule, sel("frame"));
  out.left = inner.origin.x;
  out.right = outer.size.width - inner.origin.x - inner.size.width;
  out.bottom = inner.origin.y;
  out.top = outer.size.height - inner.origin.y - inner.size.height;
  return out;
}
const char *fd_test_new_card_title(void) { return cstring(send0(newCardDrawn, sel("title"))); }
const char *fd_test_theme_title(void) { return cstring(send0(themeDrawn, sel("title"))); }
double fd_test_level_value(int i) { return sendDouble0(levelsDrawn[i], sel("doubleValue")); }

// The capsules drawn, in order: the tabs, the new card button, each limit, the
// compact limits when there are limits, the theme and its icon.
static id slot(int i, const char **name) {
  const char *n = "";
  id v = (id)0;
  if (i == 0) {
    n = "tabs", v = tabsCapsule;
  } else if (i == 1) {
    n = "newCard", v = newCardCapsule;
  } else if (i < 2 + limitsDrawn) {
    n = "limit", v = limitCapsules[i - 2];
  } else {
    int rest = i - 2 - limitsDrawn - (compactCapsule ? 1 : 0);
    if (rest < 0) {
      n = "compactLimits", v = compactCapsule;
    } else if (rest == 0) {
      n = "theme", v = themeCapsule;
    } else if (rest == 1) {
      n = "themeIcon", v = themeIconCapsule;
    }
  }
  if (name) *name = n;
  return v;
}

int fd_test_capsule_slots(void) { return 2 + limitsDrawn + (compactCapsule ? 1 : 0) + 2; }

const char *fd_test_capsule_slot_name(int i) {
  const char *name;
  slot(i, &name);
  return name;
}

fd_capsule_frame fd_test_capsule_slot_frame(int i) {
  id v = slot(i, NULL);
  CGRect r = v ? sendRect0(v, sel("frame")) : CGRectZero;
  fd_capsule_frame out = {r.origin.x, r.size.width, v && !((signed char (*)(id, SEL))objc_msgSend)(v, sel("isHidden"))};
  return out;
}

double fd_test_row_width(void) { return rowDrawn ? sendRect0(rowDrawn, sel("bounds")).size.width : -1; }

double fd_test_min_content_width(void *container) {
  id window = send0((id)container, sel("window"));
  return window ? sendSize0(window, sel("contentMinSize")).width : -1;
}

const char *fd_test_compact_text(void) { return cstring(send0(compactLabelDrawn, sel("stringValue"))); }
const char *fd_test_compact_tooltip(void) { return cstring(send0(compactLabelDrawn, sel("toolTip"))); }
const char *fd_test_theme_icon_title(void) { return cstring(send0(themeIconDrawn, sel("title"))); }
const char *fd_test_theme_icon_tooltip(void) { return cstring(send0(themeIconDrawn, sel("toolTip"))); }

const char *fd_test_theme_icon_accessibility_label(void) {
  return cstring(send0(themeIconDrawn, sel("accessibilityLabel")));
}

int fd_test_row_passes_through(void *container) {
  id parent = (id)container;
  id rowView = ((id (*)(id, SEL, unsigned long))objc_msgSend)(send0(parent, sel("subviews")), sel("objectAtIndex:"), 0);
  CGRect r = sendRect0(rowView, sel("frame"));
  // Between the new card capsule and the theme: over the board.
  CGPoint gap = CGPointMake(r.origin.x + r.size.width / 2, r.origin.y + r.size.height / 2);
  return ((id (*)(id, SEL, CGPoint))objc_msgSend)(rowView, sel("hitTest:"), gap) == (id)0;
}

static void sendAction(id control) {
  ((signed char (*)(id, SEL, SEL, id))objc_msgSend)(control, sel("sendAction:to:"),
                                                    ((SEL (*)(id, SEL))objc_msgSend)(control, sel("action")),
                                                    send0(control, sel("target")));
}

void fd_test_press_segment(int i) {
  sendVoidLong(segmentedDrawn, sel("setSelectedSegment:"), i);
  sendAction(segmentedDrawn);
}

void fd_test_press_new_card(void) { sendAction(newCardDrawn); }

const char *fd_test_capsule_slot_appearance(int i) {
  id wrapper = slot(i, NULL);
  if (!wrapper) return "";
  id content = respondsTo(wrapper, "contentView")
                   ? send0(wrapper, sel("contentView"))
                   : ((id (*)(id, SEL, unsigned long))objc_msgSend)(send0(wrapper, sel("subviews")),
                                                                    sel("objectAtIndex:"), 0);
  return appearanceName(send0(content, sel("appearance")));
}

const char *fd_test_app_appearance(void) {
  return appearanceName(send0(send0(cls("NSApplication"), sel("sharedApplication")), sel("appearance")));
}

void fd_test_set_app_appearance(const char *name) {
  id appearance = name && *name ? send1(cls("NSAppearance"), sel("appearanceNamed:"), nsstring(name)) : (id)0;
  sendVoid1(send0(cls("NSApplication"), sel("sharedApplication")), sel("setAppearance:"), appearance);
}
void fd_test_press_theme_icon(void) { sendAction(themeIconDrawn); }

double fd_test_capsule_slot_background_brightness(int i) {
  id wrapper = slot(i, NULL);
  if (!wrapper) return -1;
  CGColorRef colour = ((CGColorRef(*)(id, SEL))objc_msgSend)(send0(wrapper, sel("layer")), sel("backgroundColor"));
  if (!colour) return -1;
  size_t n = CGColorGetNumberOfComponents(colour);
  const CGFloat *c = CGColorGetComponents(colour);
  return n >= 3 ? (c[0] + c[1] + c[2]) / 3 : n >= 1 ? c[0] : -1;
}

// Whether a capsule's control is drawn inside its glass: a descendant of the
// contentView of the nearest NSGlassEffectView above it. which is as below.
int fd_test_capsule_inside_glass(int which) {
  id control = which == 0 ? segmentedDrawn : which == 1 ? newCardDrawn : themeDrawn;
  id glassClass = cls("NSGlassEffectView");
  if (!control || !glassClass) return 0;
  for (id v = send0(control, sel("superview")); v; v = send0(v, sel("superview"))) {
    if (((signed char (*)(id, SEL, id))objc_msgSend)(v, sel("isKindOfClass:"), glassClass)) {
      id content = send0(v, sel("contentView"));
      return content && ((signed char (*)(id, SEL, id))objc_msgSend)(control, sel("isDescendantOf:"), content) != 0;
    }
  }
  return 0;
}

// A click at the middle of a capsule's control, hit-tested the way the window
// hit-tests a mouse down: from its content view down. which is 0 for the tabs,
// 1 for the new card button, 2 for the theme button.
int fd_test_click_reaches_capsule(int which) {
  id control = which == 0 ? segmentedDrawn : which == 1 ? newCardDrawn : themeDrawn;
  id window = send0(control, sel("window"));
  if (!window) return 0;
  // Laid out first, as a window on screen is before any click: until then a
  // glass's content view has no size, and a hit test stops at the glass.
  sendVoid0(send0(window, sel("contentView")), sel("layoutSubtreeIfNeeded"));
  CGRect b = sendRect0(control, sel("bounds"));
  CGPoint mid = CGPointMake(b.origin.x + b.size.width / 2, b.origin.y + b.size.height / 2);
  CGPoint inWindow =
      ((CGPoint(*)(id, SEL, CGPoint, id))objc_msgSend)(control, sel("convertPoint:toView:"), mid, (id)0);
  id content = send0(window, sel("contentView"));
  // hitTest: takes the point in the content view's superview, whose coordinates
  // are the window's.
  id superview = send0(content, sel("superview"));
  CGPoint inSuper = superview ? ((CGPoint(*)(id, SEL, CGPoint, id))objc_msgSend)(superview, sel("convertPoint:fromView:"),
                                                                                  inWindow, (id)0)
                              : inWindow;
  id hit = ((id (*)(id, SEL, CGPoint))objc_msgSend)(content, sel("hitTest:"), inSuper);
  return hit && ((signed char (*)(id, SEL, id))objc_msgSend)(hit, sel("isDescendantOf:"), control) != 0;
}
