// The capsule row: tabs, new card, theme and limits as system controls in
// glass, drawn from the board's model (capsulemodel.go). Raw runtime calls, as
// frame_darwin.c.
//
// The row is one NSGlassEffectContainerView, the size of the row, so the glass
// of neighbouring capsules merges (spec 5.1). It passes a click on to the board
// wherever no capsule is under the pointer: the gaps between capsules are over
// the board.
#include "capsules_darwin.h"

#include "_cgo_export.h"

#include <CoreGraphics/CGGeometry.h>
#include <objc/message.h>
#include <objc/objc.h>
#include <objc/runtime.h>
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
static long sendLong0(id recv, SEL s) { return ((long (*)(id, SEL))objc_msgSend)(recv, s); }
static double sendDouble0(id recv, SEL s) { return ((double (*)(id, SEL))objc_msgSend)(recv, s); }

static CGRect sendRect0(id recv, SEL s) {
#if defined(__x86_64__)
  CGRect r;
  ((void (*)(CGRect *, id, SEL))objc_msgSend_stret)(&r, recv, s);
  return r;
#else
  return ((CGRect (*)(id, SEL))objc_msgSend)(recv, s);
#endif
}

static CGSize fittingSize(id view) { return ((CGSize (*)(id, SEL))objc_msgSend)(view, sel("fittingSize")); }

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

// --- the row: a container that lets clicks through its gaps ---------------------

static Class rowSuper;

static id rowHitTest(id self, SEL _cmd, CGPoint point) {
  struct objc_super up = {self, rowSuper};
  id hit = ((id (*)(struct objc_super *, SEL, CGPoint))objc_msgSendSuper)(&up, _cmd, point);
  if (hit == self) return (id)0;
  if (respondsTo(self, "contentView") && hit == send0(self, sel("contentView"))) return (id)0;
  return hit;
}

static Class rowClass(void) {
  static Class klass;
  if (klass) return klass;
  rowSuper = (Class)objc_getClass("NSGlassEffectContainerView");
  if (!rowSuper) rowSuper = (Class)objc_getClass("NSView");
  klass = objc_allocateClassPair(rowSuper, "FleetdeckCapsuleRow", 0);
  class_addMethod(klass, sel("hitTest:"), (IMP)rowHitTest, "@@:{CGPoint=dd}");
  objc_registerClassPair(klass);
  return klass;
}

// --- presses -------------------------------------------------------------------

enum { tagTabs = 1, tagNewCard = 2, tagTheme = 3 };

#define maxTabs 8
#define maxLimits 8

// What the last draw made: the tabs' ids for a press, and the controls for the
// tests to read.
static char *tabIDsDrawn[maxTabs];
static int tabsDrawn;
static id segmentedDrawn, newCardDrawn, themeDrawn;
static id levelsDrawn[maxLimits];
static int capsulesDrawn;

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

// A capsule of the row's material around content, width fitted to it.
static id capsule(const char *mode, id content, double x, long autoresizing) {
  CGSize fit = fittingSize(content);
  CGRect frame = CGRectMake(x, 0, fit.width + 2 * capsulePadding, capsuleHeight);
  CGRect inner = CGRectMake(capsulePadding, (capsuleHeight - fit.height) / 2, fit.width, fit.height);
  id wrapper;
  id holder = initWithFrame(cls("NSView"), CGRectMake(0, 0, frame.size.width, frame.size.height));
  sendVoidRect(content, sel("setFrame:"), inner);
  sendVoid1(holder, sel("addSubview:"), content);
  if (strcmp(mode, "glass") == 0 && cls("NSGlassEffectView")) {
    wrapper = initWithFrame(cls("NSGlassEffectView"), frame);
    sendVoidLong(wrapper, sel("setStyle:"), 0);
    sendVoidDouble(wrapper, sel("setCornerRadius:"), capsuleHeight / 2);
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
      id colour = send0(cls("NSColor"), sel("controlBackgroundColor"));
      ((void (*)(id, SEL, void *))objc_msgSend)(layer, sel("setBackgroundColor:"),
                                                ((void *(*)(id, SEL))objc_msgSend)(colour, sel("CGColor")));
    }
    sendVoid1(wrapper, sel("addSubview:"), holder);
  }
  sendVoid0(holder, sel("release"));
  sendVoidLong(wrapper, sel("setAutoresizingMask:"), autoresizing);
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

void fd_capsules_clear(void *container) {
  void *pool = objc_autoreleasePoolPush();
  id old = send0(send0((id)container, sel("subviews")), sel("copy"));
  for (long i = 0, n = sendLong0(old, sel("count")); i < n; i++) {
    sendVoid0(((id (*)(id, SEL, unsigned long))objc_msgSend)(old, sel("objectAtIndex:"), (unsigned long)i),
              sel("removeFromSuperview"));
  }
  sendVoid0(old, sel("release"));
  capsulesDrawn = 0;
  objc_autoreleasePoolPop(pool);
}

void fd_capsules_draw(void *container, const char *mode, const char **tabIDs, const char **tabLabels, int tabCount,
                      int selectedTab, const char *newCardLabel, const char *themeLabel, const char **limitLabels,
                      const char **limitTexts, const double *limitValues, const double *limitRGB, int limitCount) {
  void *pool = objc_autoreleasePoolPush();
  id parent = (id)container;
  fd_capsules_clear(container);

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
  capsulesDrawn = 0;
  double x = 0;

  segmentedDrawn = ((id (*)(id, SEL, id, long, id, SEL))objc_msgSend)(
      cls("NSSegmentedControl"), sel("segmentedControlWithLabels:trackingMode:target:action:"), labels,
      0 /* select one */, target(), sel("pressed:"));
  sendVoidLong(segmentedDrawn, sel("setTag:"), tagTabs);
  sendVoidLong(segmentedDrawn, sel("setSelectedSegment:"), selectedTab);
  id c = capsule(mode, segmentedDrawn, x, 4 /* max-x margin: stays left */);
  x += sendRect0(c, sel("frame")).size.width + capsuleGap;
  adopt(into, c);
  capsulesDrawn++;

  newCardDrawn = button(newCardLabel, tagNewCard);
  c = capsule(mode, newCardDrawn, x, 4);
  adopt(into, c);
  capsulesDrawn++;

  // From the right edge leftwards: the last limit first, the theme last.
  double right = bounds.size.width;
  int limits = limitCount < maxLimits ? limitCount : maxLimits;
  for (int i = limits - 1; i >= 0; i--) {
    id level = initWithFrame(cls("NSLevelIndicator"), CGRectMake(0, 0, 44, 8));
    sendVoidLong(level, sel("setLevelIndicatorStyle:"), 1);  // continuous capacity
    sendVoidDouble(level, sel("setMinValue:"), 0);
    sendVoidDouble(level, sel("setMaxValue:"), 100);
    sendVoidDouble(level, sel("setDoubleValue:"), limitValues[i]);
    if (limitRGB[3 * i] >= 0) {
      id colour = ((id (*)(id, SEL, double, double, double, double))objc_msgSend)(
          cls("NSColor"), sel("colorWithSRGBRed:green:blue:alpha:"), limitRGB[3 * i], limitRGB[3 * i + 1],
          limitRGB[3 * i + 2], 1.0);
      if (respondsTo(level, "setFillColor:")) sendVoid1(level, sel("setFillColor:"), colour);
    }
    levelsDrawn[i] = level;
    id views[3] = {label(limitLabels[i]), level, label(limitTexts[i])};
    id content = row(views, 3);
    sendVoid0(level, sel("release"));
    CGSize fit = fittingSize(content);
    right -= fit.width + 2 * capsulePadding;
    c = capsule(mode, content, right, 1 /* min-x margin: stays right */);
    adopt(into, c);
    capsulesDrawn++;
    right -= capsuleGap;
  }

  themeDrawn = button(themeLabel, tagTheme);
  CGSize themeFit = fittingSize(themeDrawn);
  right -= themeFit.width + 2 * capsulePadding;
  c = capsule(mode, themeDrawn, right, 1);
  adopt(into, c);
  capsulesDrawn++;

  objc_autoreleasePoolPop(pool);
}

// --- for the tests ---------------------------------------------------------------

int fd_test_capsule_count(void) { return capsulesDrawn; }

const char *fd_test_segment_label(int i) {
  return cstring(((id (*)(id, SEL, long))objc_msgSend)(segmentedDrawn, sel("labelForSegment:"), i));
}

int fd_test_selected_segment(void) { return (int)sendLong0(segmentedDrawn, sel("selectedSegment")); }
const char *fd_test_new_card_title(void) { return cstring(send0(newCardDrawn, sel("title"))); }
const char *fd_test_theme_title(void) { return cstring(send0(themeDrawn, sel("title"))); }
double fd_test_level_value(int i) { return sendDouble0(levelsDrawn[i], sel("doubleValue")); }

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
