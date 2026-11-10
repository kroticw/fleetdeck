// The bar across the window's top: the brand, the fleet with its menu, the
// limits, the update, the counters and the theme, as system controls in glass,
// drawn from the board's model (capsulemodel.go). Raw runtime calls, as
// frame_darwin.c.
//
// The row is one NSGlassEffectContainerView across the bar, so the glass of
// neighbouring capsules merges (spec 5.1). It passes a click on wherever no
// capsule is under the pointer: the gaps are the band the window is dragged by.
//
// A row too narrow for every part hides the counters, and never lets two parts
// overlap. The row's narrowest form is its minimum, which the window keeps room
// for.
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


static id sendDouble1(id recv, SEL s, double v) { return ((id (*)(id, SEL, double))objc_msgSend)(recv, s, v); }

// --- what the last draw made -----------------------------------------------------

enum { tagFleet = 1, tagLimits = 2, tagUpdate = 3, tagTheme = 4 };
// Menu items: a fleet's index, or one of these.
enum { itemAll = -1, itemAdd = -2 };

#define maxLimits 8
#define maxCounters 4
#define maxFleets 32

// The parts and their controls, for placing them as the row's width changes and
// for the tests to read. Every capsule is the row's subview, so a cleared row
// takes them all and they are forgotten here.
static id rowDrawn;
static id brandView, fleetCapsule, limitsCapsule, updateCapsule, themeCapsule;
static id counterViews[maxCounters];
static int countersDrawn;
static id brandIconDrawn, brandNameDrawn, brandVersionDrawn;
static id fleetDotDrawn, fleetNameDrawn, fleetOpenerDrawn;
static id limitsLabelDrawn, busyNameDrawn, busyTextDrawn, limitsOpenerDrawn;
static id dotsDrawn[maxLimits];
static int limitsDrawn;
static id updateTextDrawn, updateButtonDrawn;
static id counterTextsDrawn[maxCounters], counterDotsDrawn[maxCounters];
static int counterHollowDrawn[maxCounters];
static id themeDrawn;
// The fleet's menu, built at each press from what the last draw had.
static char *fleetNamesDrawn[maxFleets];
static int fleetWaitingDrawn[maxFleets], fleetCurrentDrawn[maxFleets], fleetsDrawn;
static char *fleetAllDrawn, *fleetAddDrawn;
// capsulesDrawn: how many parts the row shows.
static int capsulesDrawn;

static void placeCapsules(double rowWidth);
static id appAppearance(void);
static void showFleetMenu(void);

static char *keep(char *old, const char *s) {
  free(old);
  return strdup(s ? s : "");
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

static const char *themeChoices[3] = {"light", "system", "dark"};

static void pressWith(const char *prefix, const char *rest) {
  char action[256];
  snprintf(action, sizeof action, "%s%s", prefix, rest);
  fleetdeckCapsulePressed(action);
}

static void capsulePressed(id self, SEL _cmd, id sender) {
  (void)self;
  (void)_cmd;
  switch (sendLong0(sender, sel("tag"))) {
    case tagFleet:
      showFleetMenu();
      return;
    case tagLimits:
      fleetdeckCapsulePressed((char *)"limits");
      return;
    case tagUpdate:
      fleetdeckCapsulePressed((char *)"update");
      return;
    case tagTheme: {
      long i = sendLong0(sender, sel("selectedSegment"));
      if (i >= 0 && i < 3) pressWith("theme:", themeChoices[i]);
      return;
    }
  }
}

static void menuPicked(id self, SEL _cmd, id item) {
  (void)self;
  (void)_cmd;
  long tag = sendLong0(item, sel("tag"));
  if (tag == itemAll) {
    fleetdeckCapsulePressed((char *)"fleets");
  } else if (tag == itemAdd) {
    fleetdeckCapsulePressed((char *)"newFleet");
  } else if (tag >= 0 && tag < fleetsDrawn) {
    pressWith("fleet:", fleetNamesDrawn[tag]);
  }
}

static id target(void) {
  static id instance;
  if (instance) return instance;
  Class klass = objc_allocateClassPair((Class)objc_getClass("NSObject"), "FleetdeckCapsuleTarget", 0);
  class_addMethod(klass, sel("pressed:"), (IMP)capsulePressed, "v@:@");
  class_addMethod(klass, sel("picked:"), (IMP)menuPicked, "v@:@");
  objc_registerClassPair(klass);
  instance = send0((id)klass, sel("new"));
  return instance;
}

// --- the fleet's menu ------------------------------------------------------------

// The tests keep the menu instead of showing it: a shown menu runs its own loop
// until it is dismissed.
static int captureMenus;
static id menuCaptured;
static CGPoint menuCapturedAt;

static id menuItem(const char *title, long tag) {
  id item = ((id (*)(id, SEL, id, SEL, id))objc_msgSend)(send0(cls("NSMenuItem"), sel("alloc")),
                                                         sel("initWithTitle:action:keyEquivalent:"), nsstring(title),
                                                         sel("picked:"), nsstring(""));
  sendVoid1(item, sel("setTarget:"), target());
  sendVoidLong(item, sel("setTag:"), tag);
  return item;
}

static void addItem(id menu, id item) {
  sendVoid1(menu, sel("addItem:"), item);
  sendVoid0(item, sel("release"));
}

// Every fleet, the one shown checked and the waiting ones with their count as
// the item's badge (macOS 14) or in its title before it; then All and Add.
static id fleetMenu(void) {
  id menu = send1(send0(cls("NSMenu"), sel("alloc")), sel("initWithTitle:"), nsstring(""));
  sendVoidBool(menu, sel("setAutoenablesItems:"), 0);
  id badgeClass = cls("NSMenuItemBadge");
  for (int i = 0; i < fleetsDrawn; i++) {
    char title[300];
    snprintf(title, sizeof title, "%s", fleetNamesDrawn[i]);
    if (fleetWaitingDrawn[i] > 0 && !badgeClass) {
      snprintf(title, sizeof title, "%s  (%d)", fleetNamesDrawn[i], fleetWaitingDrawn[i]);
    }
    id item = menuItem(title, i);
    sendVoidLong(item, sel("setState:"), fleetCurrentDrawn[i] ? 1 : 0);
    if (fleetWaitingDrawn[i] > 0 && badgeClass && respondsTo(item, "setBadge:")) {
      id badge = ((id (*)(id, SEL, long))objc_msgSend)(send0(badgeClass, sel("alloc")), sel("initWithCount:"),
                                                       (long)fleetWaitingDrawn[i]);
      sendVoid1(item, sel("setBadge:"), badge);
      sendVoid0(badge, sel("release"));
    }
    addItem(menu, item);
  }
  sendVoid1(menu, sel("addItem:"), send0(cls("NSMenuItem"), sel("separatorItem")));
  addItem(menu, menuItem(fleetAllDrawn, itemAll));
  addItem(menu, menuItem(fleetAddDrawn, itemAdd));
  return menu;
}

// The menu opens under the fleet's capsule, its left edge on the capsule's: in
// 1.8.0 the page's menu opened at the right of its button.
static CGPoint underFleet(void) {
  CGRect b = sendRect0(fleetCapsule, sel("bounds"));
  int flipped = ((signed char (*)(id, SEL))objc_msgSend)(fleetCapsule, sel("isFlipped")) != 0;
  return CGPointMake(0, flipped ? b.size.height + 6 : -6);
}

static void showFleetMenu(void) {
  if (!fleetCapsule) return;
  id menu = fleetMenu();
  CGPoint at = underFleet();
  if (captureMenus) {
    if (menuCaptured) sendVoid0(menuCaptured, sel("release"));
    menuCaptured = menu;
    menuCapturedAt = ((CGPoint(*)(id, SEL, CGPoint, id))objc_msgSend)(fleetCapsule, sel("convertPoint:toView:"), at, (id)0);
    return;
  }
  ((signed char (*)(id, SEL, id, CGPoint, id))objc_msgSend)(menu, sel("popUpMenuPositioningItem:atLocation:inView:"),
                                                            (id)0, at, fleetCapsule);
  sendVoid0(menu, sel("release"));
}

// --- capsules ------------------------------------------------------------------

static const double capsuleHeight = 32, capsulePadding = 12, capsuleGap = 8;
static const double pillHeight = 24, pillPadding = 10;
// The bar's right margin past the row (geometry.go panelMargin).
static const double rowRightMargin = 10;

// A view holding content, as wide as content fits plus padding on either side,
// height tall, content centred in it.
static id holderFor(id content, double padding, double height) {
  CGSize fit = fittingSize(content);
  id holder = initWithFrame(cls("NSView"), CGRectMake(0, 0, fit.width + 2 * padding, height));
  sendVoidRect(content, sel("setFrame:"), CGRectMake(padding, (height - fit.height) / 2, fit.width, fit.height));
  sendVoid1(holder, sel("addSubview:"), content);
  return holder;
}

// A capsule of the row's material around content, width fitted to it with
// padding on either side. The row places it (placeCapsules); it never moves by
// autoresizing.
static id capsule(const char *mode, id content, double padding) {
  id holder = holderFor(content, padding, capsuleHeight);
  CGRect frame = sendRect0(holder, sel("frame"));
  id wrapper;
  if (strcmp(mode, "glass") == 0 && cls("NSGlassEffectView")) {
    // The control is the glass's content: only NSGlassEffectView's contentView
    // is drawn inside the glass. In v0.10.0 the control lay beside the glass in
    // a plain view, and the row's NSGlassEffectContainerView, which draws its
    // glass in a view above its own content, drew the glass over the labels.
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

// A capsule that is one control from anywhere on it: a transparent button over
// the whole capsule, its content drawn underneath. What it says to a pointer and
// to VoiceOver is description.
static id coverWithOpener(id wrapper, long tag, const char *description) {
  id holder = respondsTo(wrapper, "contentView") ? send0(wrapper, sel("contentView"))
                                                 : send0(send0(wrapper, sel("subviews")), sel("firstObject"));
  id opener = button("", tag);
  sendVoidBool(opener, sel("setTransparent:"), 1);
  sendVoid1(opener, sel("setToolTip:"), nsstring(description));
  sendVoid1(opener, sel("setAccessibilityLabel:"), nsstring(description));
  sendVoidRect(opener, sel("setFrame:"), sendRect0(holder, sel("bounds")));
  sendVoidLong(opener, sel("setAutoresizingMask:"), 18);
  sendVoid1(holder, sel("addSubview:"), opener);
  return opener;
}

static id label(const char *text) {
  return send1(cls("NSTextField"), sel("labelWithString:"), nsstring(text));
}

static id systemFont(double size, double weight) {
  return ((id (*)(id, SEL, double, double))objc_msgSend)(cls("NSFont"), sel("systemFontOfSize:weight:"), size, weight);
}

static id monoFont(double size) {
  const char *s = respondsTo(cls("NSFont"), "monospacedSystemFontOfSize:weight:") ? "monospacedSystemFontOfSize:weight:"
                                                                                  : "monospacedDigitSystemFontOfSize:weight:";
  return ((id (*)(id, SEL, double, double))objc_msgSend)(cls("NSFont"), sel(s), size, 0.0);
}

// A label in font, coloured colour when there is one.
static id styled(const char *text, id font, id colour) {
  id l = label(text);
  if (font) sendVoid1(l, sel("setFont:"), font);
  if (colour) sendVoid1(l, sel("setTextColor:"), colour);
  return l;
}

static id systemColour(const char *name) { return send0(cls("NSColor"), sel(name)); }

static id row(id views[], int count, double spacing) {
  id array = ((id (*)(id, SEL, id *, unsigned long))objc_msgSend)(cls("NSArray"), sel("arrayWithObjects:count:"), views,
                                                                  (unsigned long)count);
  id stack = send1(cls("NSStackView"), sel("stackViewWithViews:"), array);
  sendVoidLong(stack, sel("setOrientation:"), 0);  // horizontal
  sendVoidDouble(stack, sel("setSpacing:"), spacing);
  return stack;
}

// rgb is three components (0..1), or -1 first for no colour.
static id srgba(const double *rgb, double alpha) {
  if (!rgb || rgb[0] < 0) return (id)0;
  return ((id (*)(id, SEL, double, double, double, double))objc_msgSend)(
      cls("NSColor"), sel("colorWithSRGBRed:green:blue:alpha:"), rgb[0], rgb[1], rgb[2], alpha);
}

static id srgb(const double *rgb) { return srgba(rgb, 1.0); }

static double widthOf(id view) { return view ? sendRect0(view, sel("frame")).size.width : 0; }

static void put(id view, double x, int shown) {
  if (!view) return;
  sendVoidPoint(view, sel("setFrameOrigin:"), CGPointMake(x, sendRect0(view, sel("frame")).origin.y));
  sendVoidBool(view, sel("setHidden:"), shown ? 0 : 1);
  if (shown) capsulesDrawn++;
}

// The width of views side by side with the row's gaps between; nil ones take
// no room.
static double groupWidth(id views[], int count) {
  double w = 0;
  int shown = 0;
  for (int i = 0; i < count; i++) {
    if (!views[i]) continue;
    w += (shown++ ? capsuleGap : 0) + widthOf(views[i]);
  }
  return w;
}

static double leftWidth(void) {
  id left[4] = {brandView, fleetCapsule, limitsCapsule, updateCapsule};
  return groupWidth(left, 4);
}

static double groupsWidth(double left, double right) { return left + (left > 0 && right > 0 ? capsuleGap : 0) + right; }

// placeCapsules lays the row out in rowWidth: the brand, the fleet, the limits
// and the update from the left edge; the theme at the right edge and the
// counters before it. A row with no room for the counters hides them; that is
// its minimum, and what is left when even it does not fit.
static void placeCapsules(double rowWidth) {
  if (!rowDrawn) return;
  // The row's width is the frame's arithmetic in floating point: a row laid out
  // at exactly a form's width still fits it.
  const double slack = 0.01;
  id right[maxCounters + 1];
  int n = 0;
  for (int i = 0; i < countersDrawn; i++) right[n++] = counterViews[i];
  right[n++] = themeCapsule;
  int counters = groupsWidth(leftWidth(), groupWidth(right, n)) <= rowWidth + slack;

  capsulesDrawn = 0;
  id left[4] = {brandView, fleetCapsule, limitsCapsule, updateCapsule};
  double x = 0;
  for (int i = 0; i < 4; i++) {
    if (!left[i]) continue;
    put(left[i], x, 1);
    x += widthOf(left[i]) + capsuleGap;
  }
  double edge = rowWidth;
  if (themeCapsule) {
    edge -= widthOf(themeCapsule);
    put(themeCapsule, edge, 1);
    edge -= capsuleGap;
  }
  for (int i = countersDrawn - 1; i >= 0; i--) {
    if (!counters) {
      put(counterViews[i], 0, 0);
      continue;
    }
    edge -= widthOf(counterViews[i]);
    put(counterViews[i], edge, 1);
    edge -= capsuleGap;
  }
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
  brandView = fleetCapsule = limitsCapsule = updateCapsule = themeCapsule = (id)0;
  brandIconDrawn = brandNameDrawn = brandVersionDrawn = (id)0;
  fleetDotDrawn = fleetNameDrawn = fleetOpenerDrawn = (id)0;
  limitsLabelDrawn = busyNameDrawn = busyTextDrawn = limitsOpenerDrawn = (id)0;
  updateTextDrawn = updateButtonDrawn = themeDrawn = (id)0;
  for (int i = 0; i < maxLimits; i++) dotsDrawn[i] = (id)0;
  for (int i = 0; i < maxCounters; i++) counterViews[i] = counterTextsDrawn[i] = counterDotsDrawn[i] = (id)0;
  limitsDrawn = countersDrawn = 0;
  capsulesDrawn = 0;
  // No row, nothing for the window to keep room for.
  setMinContentWidth(send0((id)container, sel("window")), 0);
  objc_autoreleasePoolPop(pool);
}

// --- the parts -----------------------------------------------------------------

// The app's icon, its name and its version: no capsule, as the page drew it.
static id drawBrand(const fd_bar *bar) {
  id views[3];
  int n = 0;
  id app = send0(cls("NSApplication"), sel("sharedApplication"));
  id icon = send0(app, sel("applicationIconImage"));
  if (icon && respondsTo(cls("NSImageView"), "imageViewWithImage:")) {
    brandIconDrawn = send1(cls("NSImageView"), sel("imageViewWithImage:"), icon);
    sendVoidLong(brandIconDrawn, sel("setImageScaling:"), 3);  // proportionally up or down
    sendVoidBool(brandIconDrawn, sel("setTranslatesAutoresizingMaskIntoConstraints:"), 0);
    views[n++] = brandIconDrawn;
  }
  brandNameDrawn = styled(bar->brandName, systemFont(14, 0.3), (id)0);
  views[n++] = brandNameDrawn;
  brandVersionDrawn = styled(bar->brandVersion, monoFont(11), systemColour("tertiaryLabelColor"));
  views[n++] = brandVersionDrawn;
  id content = row(views, n, 8);
  if (brandIconDrawn) {
    for (int i = 0; i < 2; i++) {
      id anchor = send0(brandIconDrawn, sel(i == 0 ? "widthAnchor" : "heightAnchor"));
      id c = sendDouble1(anchor, sel("constraintEqualToConstant:"), 24);
      sendVoidBool(c, sel("setActive:"), 1);
    }
  }
  id holder = holderFor(content, 4, capsuleHeight);
  return holder;
}

static id drawFleet(const fd_bar *bar) {
  fleetDotDrawn = styled("■", systemFont(9, 0), srgb(bar->accentRGB));
  fleetNameDrawn = label(bar->fleetName);
  id views[3] = {fleetDotDrawn, fleetNameDrawn, label("▾")};
  id c = capsule(bar->mode, row(views, 3, 7), capsulePadding);
  fleetOpenerDrawn = coverWithOpener(c, tagFleet, bar->fleetLabel);
  fleetsDrawn = bar->fleetCount < maxFleets ? bar->fleetCount : maxFleets;
  for (int i = 0; i < fleetsDrawn; i++) {
    fleetNamesDrawn[i] = keep(fleetNamesDrawn[i], bar->fleetNames[i]);
    fleetWaitingDrawn[i] = bar->fleetWaiting[i];
    fleetCurrentDrawn[i] = bar->fleetCurrent[i];
  }
  fleetAllDrawn = keep(fleetAllDrawn, bar->fleetAll);
  fleetAddDrawn = keep(fleetAddDrawn, bar->fleetAdd);
  return c;
}

// The limits' word, a dot per limit in its level's colour -- a level indicator
// drew nothing on glass -- and the busiest one's name and number in its colour.
// The whole capsule opens the limits menu.
static id drawLimits(const fd_bar *bar) {
  limitsDrawn = bar->limitCount < maxLimits ? bar->limitCount : maxLimits;
  if (limitsDrawn == 0) return (id)0;
  id dots[maxLimits];
  for (int i = 0; i < limitsDrawn; i++) {
    dotsDrawn[i] = styled("●", systemFont(8, 0), srgb(&bar->limitRGB[3 * i]));
    dots[i] = dotsDrawn[i];
  }
  limitsLabelDrawn = styled(bar->limitsLabel, (id)0, systemColour("secondaryLabelColor"));
  busyNameDrawn = label(bar->busyName);
  busyTextDrawn = styled(bar->busyText, monoFont(11.5), srgb(bar->busyRGB));
  id views[5] = {limitsLabelDrawn, row(dots, limitsDrawn, 3), busyNameDrawn, busyTextDrawn, label("▾")};
  id c = capsule(bar->mode, row(views, 5, 7), capsulePadding);
  limitsOpenerDrawn = coverWithOpener(c, tagLimits, bar->limitsTooltip);
  return c;
}

static id drawUpdate(const fd_bar *bar) {
  if (!bar->updateText || !*bar->updateText) return (id)0;
  updateTextDrawn = label(bar->updateText);
  updateButtonDrawn = ((id (*)(id, SEL, id, id, SEL))objc_msgSend)(
      cls("NSButton"), sel("buttonWithTitle:target:action:"), nsstring(bar->updateAction), target(), sel("pressed:"));
  sendVoidLong(updateButtonDrawn, sel("setTag:"), tagUpdate);
  sendVoidLong(updateButtonDrawn, sel("setControlSize:"), 1);  // small
  id accent = srgb(bar->accentRGB);
  if (accent && respondsTo(updateButtonDrawn, "setBezelColor:")) sendVoid1(updateButtonDrawn, sel("setBezelColor:"), accent);
  id views[3] = {styled("●", systemFont(7, 0), accent), updateTextDrawn, updateButtonDrawn};
  return capsule(bar->mode, row(views, 3, 7), capsulePadding);
}

// A counter: no glass, its colour's tint under its dot and text, as the page's
// pills.
static id drawCounter(const fd_bar *bar, int i) {
  const double *rgb = &bar->counterRGB[3 * i];
  counterHollowDrawn[i] = bar->counterHollow[i];
  counterDotsDrawn[i] = styled(bar->counterHollow[i] ? "○" : "●", systemFont(8, 0), srgb(rgb));
  counterTextsDrawn[i] = styled(bar->counterTexts[i], systemFont(12, 0.23), srgb(rgb));
  id views[2] = {counterDotsDrawn[i], counterTextsDrawn[i]};
  id pill = holderFor(row(views, 2, 6), pillPadding, pillHeight);
  sendVoidBool(pill, sel("setWantsLayer:"), 1);
  id layer = send0(pill, sel("layer"));
  sendVoidDouble(layer, sel("setCornerRadius:"), pillHeight / 2);
  id tint = srgba(rgb, 0.14);
  if (tint) {
    ((void (*)(id, SEL, void *))objc_msgSend)(layer, sel("setBackgroundColor:"),
                                              ((void *(*)(id, SEL))objc_msgSend)(tint, sel("CGColor")));
  }
  CGRect f = sendRect0(pill, sel("frame"));
  f.origin.y = (capsuleHeight - pillHeight) / 2;
  sendVoidRect(pill, sel("setFrame:"), f);
  return pill;
}

// The theme as three segments -- light, the system's, dark -- each an SF
// Symbol where the system has them, its name a tooltip.
static id drawTheme(const fd_bar *bar) {
  static const char *symbols[3] = {"sun.max", "display", "moon"};
  static const char *marks[3] = {"☀", "◐", "☾"};
  id images = send0(cls("NSMutableArray"), sel("array"));
  int asImages = respondsTo(cls("NSImage"), "imageWithSystemSymbolName:accessibilityDescription:");
  for (int i = 0; i < 3 && asImages; i++) {
    id image = ((id (*)(id, SEL, id, id))objc_msgSend)(cls("NSImage"), sel("imageWithSystemSymbolName:accessibilityDescription:"),
                                                       nsstring(symbols[i]), nsstring(bar->themeLabels[i]));
    if (!image) {
      asImages = 0;
      break;
    }
    sendVoid1(images, sel("addObject:"), image);
  }
  if (asImages) {
    themeDrawn = ((id (*)(id, SEL, id, long, id, SEL))objc_msgSend)(
        cls("NSSegmentedControl"), sel("segmentedControlWithImages:trackingMode:target:action:"), images, 0, target(),
        sel("pressed:"));
  } else {
    id labels = send0(cls("NSMutableArray"), sel("array"));
    for (int i = 0; i < 3; i++) sendVoid1(labels, sel("addObject:"), nsstring(marks[i]));
    themeDrawn = ((id (*)(id, SEL, id, long, id, SEL))objc_msgSend)(
        cls("NSSegmentedControl"), sel("segmentedControlWithLabels:trackingMode:target:action:"), labels, 0, target(),
        sel("pressed:"));
  }
  sendVoidLong(themeDrawn, sel("setTag:"), tagTheme);
  for (int i = 0; i < 3; i++) {
    ((void (*)(id, SEL, id, long))objc_msgSend)(themeDrawn, sel("setToolTip:forSegment:"), nsstring(bar->themeLabels[i]), i);
  }
  sendVoidLong(themeDrawn, sel("setSelectedSegment:"), bar->themeSegment);
  sendVoid1(themeDrawn, sel("setAccessibilityLabel:"), nsstring(bar->themeLabel));
  // A capsule in the capsule, from macOS 26: with the same room on every side
  // the two are concentric.
  double padding = capsulePadding;
  if (respondsTo(themeDrawn, "setBorderShape:")) {
    sendVoidLong(themeDrawn, sel("setBorderShape:"), 1);  // NSControlBorderShapeCapsule
    padding = (capsuleHeight - fittingSize(themeDrawn).height) / 2;
  }
  id accent = srgb(bar->accentRGB);
  if (accent && respondsTo(themeDrawn, "setSelectedSegmentBezelColor:"))
    sendVoid1(themeDrawn, sel("setSelectedSegmentBezelColor:"), accent);
  return capsule(bar->mode, themeDrawn, padding);
}

double fd_capsules_draw(void *container, const fd_bar *bar) {
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
  snprintf(now, sizeof now, "%s, in %s", bar->mode, appearanceName(appAppearance()));
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

  brandView = drawBrand(bar);
  adopt(into, brandView);
  fleetCapsule = drawFleet(bar);
  adopt(into, fleetCapsule);
  limitsCapsule = drawLimits(bar);
  if (limitsCapsule) adopt(into, limitsCapsule);
  updateCapsule = drawUpdate(bar);
  if (updateCapsule) adopt(into, updateCapsule);
  countersDrawn = bar->counterCount < maxCounters ? bar->counterCount : maxCounters;
  for (int i = 0; i < countersDrawn; i++) {
    counterViews[i] = drawCounter(bar, i);
    adopt(into, counterViews[i]);
  }
  themeCapsule = drawTheme(bar);
  adopt(into, themeCapsule);

  rowDrawn = rowView;
  placeCapsules(bounds.size.width);

  double rowMin = groupsWidth(leftWidth(), widthOf(themeCapsule));
  // The row runs across the bar from past the window's buttons to its right
  // margin, the panels under it: the window is as wide as the wider of the
  // two asks.
  double rowAsks = sendRect0(parent, sel("frame")).origin.x + rowMin + rowRightMargin;
  double minWidth = rowAsks > bar->frameMinWidth ? rowAsks : bar->frameMinWidth;
  setMinContentWidth(send0(parent, sel("window")), minWidth);
  growToMinContentWidth(send0(parent, sel("window")), minWidth);

  objc_autoreleasePoolPop(pool);
  return rowMin;
}

// --- for the tests ---------------------------------------------------------------

int fd_test_capsule_count(void) { return capsulesDrawn; }

// The parts in order: the brand, the fleet, the limits, the update, each
// counter, the theme.
static id slot(int i, const char **name) {
  const char *n = "";
  id v = (id)0;
  if (i == 0) {
    n = "brand", v = brandView;
  } else if (i == 1) {
    n = "fleet", v = fleetCapsule;
  } else if (i == 2) {
    n = "limits", v = limitsCapsule;
  } else if (i == 3) {
    n = "update", v = updateCapsule;
  } else if (i < 4 + countersDrawn) {
    n = "counter", v = counterViews[i - 4];
  } else if (i == 4 + countersDrawn) {
    n = "theme", v = themeCapsule;
  }
  if (name) *name = n;
  return v;
}

int fd_test_capsule_slots(void) { return 5 + countersDrawn; }

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

double fd_test_capsule_slot_y(int i) {
  id v = slot(i, NULL);
  return v ? sendRect0(v, sel("frame")).origin.y : -1;
}

double fd_test_capsule_slot_h(int i) {
  id v = slot(i, NULL);
  return v ? sendRect0(v, sel("frame")).size.height : -1;
}

double fd_test_row_width(void) { return rowDrawn ? sendRect0(rowDrawn, sel("bounds")).size.width : -1; }

double fd_test_min_content_width(void *container) {
  id window = send0((id)container, sel("window"));
  return window ? sendSize0(window, sel("contentMinSize")).width : -1;
}

int fd_test_row_passes_through(void *container) {
  id parent = (id)container;
  id rowView = ((id (*)(id, SEL, unsigned long))objc_msgSend)(send0(parent, sel("subviews")), sel("objectAtIndex:"), 0);
  CGRect r = sendRect0(rowView, sel("frame"));
  // Between the left group and the right one: over the bar.
  CGPoint gap = CGPointMake(r.origin.x + r.size.width / 2, r.origin.y + r.size.height / 2);
  return ((id (*)(id, SEL, CGPoint))objc_msgSend)(rowView, sel("hitTest:"), gap) == (id)0;
}

// "counter3" is counter 3 of what; -1 when what is not one.
static int indexed(const char *what, const char *prefix) {
  size_t n = strlen(prefix);
  if (strncmp(what, prefix, n) != 0 || !what[n]) return -1;
  return atoi(what + n);
}

static id textView(const char *what) {
  if (!strcmp(what, "brandName")) return brandNameDrawn;
  if (!strcmp(what, "brandVersion")) return brandVersionDrawn;
  if (!strcmp(what, "fleetName")) return fleetNameDrawn;
  if (!strcmp(what, "limitsLabel")) return limitsLabelDrawn;
  if (!strcmp(what, "busyName")) return busyNameDrawn;
  if (!strcmp(what, "busyText")) return busyTextDrawn;
  if (!strcmp(what, "updateText")) return updateTextDrawn;
  int i = indexed(what, "counter");
  if (i >= 0 && i < countersDrawn) return counterTextsDrawn[i];
  return (id)0;
}

const char *fd_test_text(const char *what) {
  if (!strcmp(what, "limitsTooltip")) return limitsOpenerDrawn ? cstring(send0(limitsOpenerDrawn, sel("toolTip"))) : "";
  if (!strcmp(what, "fleetLabel")) {
    return fleetOpenerDrawn ? cstring(send0(fleetOpenerDrawn, sel("accessibilityLabel"))) : "";
  }
  if (!strcmp(what, "updateAction")) return updateButtonDrawn ? cstring(send0(updateButtonDrawn, sel("title"))) : "";
  id v = textView(what);
  return v ? cstring(send0(v, sel("stringValue"))) : "";
}

static double greenOf(id colour) {
  if (!colour) return -1;
  id srgbSpace = send0(cls("NSColorSpace"), sel("sRGBColorSpace"));
  id c = send1(colour, sel("colorUsingColorSpace:"), srgbSpace);
  return c ? sendDouble0(c, sel("greenComponent")) : -1;
}

double fd_test_green(const char *what) {
  if (!strcmp(what, "fleetDot")) return fleetDotDrawn ? greenOf(send0(fleetDotDrawn, sel("textColor"))) : -1;
  if (!strcmp(what, "busyText")) return busyTextDrawn ? greenOf(send0(busyTextDrawn, sel("textColor"))) : -1;
  int i = indexed(what, "dot");
  if (i >= 0) return i < limitsDrawn ? greenOf(send0(dotsDrawn[i], sel("textColor"))) : -1;
  i = indexed(what, "counterTint");
  if (i >= 0) {
    if (i >= countersDrawn) return -1;
    CGColorRef colour = ((CGColorRef(*)(id, SEL))objc_msgSend)(send0(counterViews[i], sel("layer")), sel("backgroundColor"));
    if (!colour || CGColorGetNumberOfComponents(colour) < 3) return -1;
    return CGColorGetComponents(colour)[1];
  }
  i = indexed(what, "counter");
  if (i >= 0) return i < countersDrawn ? greenOf(send0(counterTextsDrawn[i], sel("textColor"))) : -1;
  return -1;
}

int fd_test_counter_hollow(int i) {
  return i < countersDrawn && !strcmp(cstring(send0(counterDotsDrawn[i], sel("stringValue"))), "○");
}

int fd_test_brand_has_icon(void) { return brandIconDrawn && send0(brandIconDrawn, sel("image")) != (id)0; }

int fd_test_theme_segments(void) { return themeDrawn ? (int)sendLong0(themeDrawn, sel("segmentCount")) : 0; }
int fd_test_theme_selected(void) { return themeDrawn ? (int)sendLong0(themeDrawn, sel("selectedSegment")) : -1; }

const char *fd_test_theme_tooltip(int i) {
  return cstring(((id (*)(id, SEL, long))objc_msgSend)(themeDrawn, sel("toolTipForSegment:"), i));
}

int fd_test_theme_segment_is_image(int i) {
  return ((id (*)(id, SEL, long))objc_msgSend)(themeDrawn, sel("imageForSegment:"), i) != (id)0;
}

long fd_test_segment_border_shape(void) {
  if (!themeDrawn || !respondsTo(themeDrawn, "borderShape")) return -1;
  return sendLong0(themeDrawn, sel("borderShape"));
}

// The theme's segments drawn into a bitmap of their own -- the control alone,
// not the screen -- and where the drawn fill starts in its top row, as a share
// of the fill's height. Drawn off screen, the control's track has nothing under
// it and only the selected segment's fill is left: a capsule's top row starts
// about half its height in, a rounded rectangle's a fifth. -1 when nothing is
// drawn.
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

double fd_test_selected_segment_top_inset(void) { return themeDrawn ? topInsetOf(themeDrawn) : -1; }

// The same measure of a stand-in for the theme's segments: the same images,
// selection, size and place, as a rounded rectangle. On a 1x screen the drawn
// capsule's top row starts nearer its edge than on a 2x one (0.375 against
// 0.46), so the control is held to this reference drawn beside it, not to a
// number. -1 on a system without border shapes.
double fd_test_rounded_segment_top_inset(void) {
  if (!themeDrawn || !respondsTo(themeDrawn, "setBorderShape:")) return -1;
  void *pool = objc_autoreleasePoolPush();
  id reference = initWithFrame(cls("NSSegmentedControl"), sendRect0(themeDrawn, sel("frame")));
  long count = sendLong0(themeDrawn, sel("segmentCount"));
  sendVoidLong(reference, sel("setSegmentCount:"), count);
  for (long i = 0; i < count; i++) {
    id image = ((id (*)(id, SEL, long))objc_msgSend)(themeDrawn, sel("imageForSegment:"), i);
    if (image) ((void (*)(id, SEL, id, long))objc_msgSend)(reference, sel("setImage:forSegment:"), image, i);
    id text = ((id (*)(id, SEL, long))objc_msgSend)(themeDrawn, sel("labelForSegment:"), i);
    if (text) ((void (*)(id, SEL, id, long))objc_msgSend)(reference, sel("setLabel:forSegment:"), text, i);
  }
  sendVoidLong(reference, sel("setSelectedSegment:"), sendLong0(themeDrawn, sel("selectedSegment")));
  sendVoidLong(reference, sel("setBorderShape:"), 2);  // NSControlBorderShapeRoundedRectangle
  sendVoidRect(reference, sel("setFrame:"), sendRect0(themeDrawn, sel("frame")));
  sendVoid1(send0(themeDrawn, sel("superview")), sel("addSubview:"), reference);
  double out = topInsetOf(reference);
  sendVoid0(reference, sel("removeFromSuperview"));
  sendVoid0(reference, sel("release"));
  objc_autoreleasePoolPop(pool);
  return out;
}

static void sendAction(id control) {
  ((signed char (*)(id, SEL, SEL, id))objc_msgSend)(control, sel("sendAction:to:"),
                                                    ((SEL (*)(id, SEL))objc_msgSend)(control, sel("action")),
                                                    send0(control, sel("target")));
}

void fd_test_press_theme(int i) {
  sendVoidLong(themeDrawn, sel("setSelectedSegment:"), i);
  sendAction(themeDrawn);
}

static id controlOf(const char *what) {
  if (!strcmp(what, "fleet")) return fleetOpenerDrawn;
  if (!strcmp(what, "limits")) return limitsOpenerDrawn;
  if (!strcmp(what, "update")) return updateButtonDrawn;
  if (!strcmp(what, "theme")) return themeDrawn;
  return (id)0;
}

void fd_test_press(const char *what) {
  id control = controlOf(what);
  if (control) sendAction(control);
}

void fd_test_capture_menus(int on) { captureMenus = on; }

static id capturedItem(int i) {
  if (!menuCaptured || i < 0 || i >= sendLong0(menuCaptured, sel("numberOfItems"))) return (id)0;
  return ((id (*)(id, SEL, long))objc_msgSend)(menuCaptured, sel("itemAtIndex:"), i);
}

int fd_test_menu_items(void) { return menuCaptured ? (int)sendLong0(menuCaptured, sel("numberOfItems")) : 0; }

const char *fd_test_menu_item_title(int i) {
  id item = capturedItem(i);
  return item ? cstring(send0(item, sel("title"))) : "";
}

int fd_test_menu_item_state(int i) {
  id item = capturedItem(i);
  return item ? (int)sendLong0(item, sel("state")) : 0;
}

int fd_test_menu_item_separator(int i) {
  id item = capturedItem(i);
  return item && ((signed char (*)(id, SEL))objc_msgSend)(item, sel("isSeparatorItem"));
}

// The badge's count where there are badges; else the count the title ends in.
long fd_test_menu_item_waiting(int i) {
  id item = capturedItem(i);
  if (!item) return 0;
  if (respondsTo(item, "badge")) {
    id badge = send0(item, sel("badge"));
    return badge ? sendLong0(badge, sel("itemCount")) : 0;
  }
  const char *title = cstring(send0(item, sel("title")));
  const char *open = strrchr(title, '(');
  return open ? atol(open + 1) : 0;
}

void fd_test_menu_pick(int i) {
  id item = capturedItem(i);
  if (item) menuPicked((id)0, (SEL)0, item);
}

static double contentHeight(id view) {
  id window = send0(view, sel("window"));
  return window ? sendRect0(send0(window, sel("contentView")), sel("bounds")).size.height : 0;
}

double fd_test_menu_x(void) { return menuCapturedAt.x; }
double fd_test_menu_y(void) { return fleetCapsule ? contentHeight(fleetCapsule) - menuCapturedAt.y : -1; }

fd_capsule_frame fd_test_fleet_in_window(void) {
  fd_capsule_frame out = {0, 0, 0};
  if (!fleetCapsule) return out;
  CGRect r = ((CGRect(*)(id, SEL, CGRect, id))objc_msgSend)(fleetCapsule, sel("convertRect:toView:"),
                                                            sendRect0(fleetCapsule, sel("bounds")), (id)0);
  out.x = r.origin.x, out.w = r.size.width, out.visible = 1;
  return out;
}

double fd_test_fleet_bottom_in_window(void) {
  if (!fleetCapsule) return -1;
  CGRect r = ((CGRect(*)(id, SEL, CGRect, id))objc_msgSend)(fleetCapsule, sel("convertRect:toView:"),
                                                            sendRect0(fleetCapsule, sel("bounds")), (id)0);
  return contentHeight(fleetCapsule) - r.origin.y;
}

int fd_test_click_reaches(const char *what) {
  id control = controlOf(what);
  if (!control) return 0;
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

// Whether a part's control is drawn inside its glass: a descendant of the
// contentView of the nearest NSGlassEffectView above it.
int fd_test_inside_glass(const char *what) {
  id control = controlOf(what);
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

const char *fd_test_capsule_slot_appearance(int i) {
  id wrapper = slot(i, NULL);
  if (!wrapper) return "";
  id content = respondsTo(wrapper, "contentView") ? send0(wrapper, sel("contentView"))
                                                  : send0(send0(wrapper, sel("subviews")), sel("firstObject"));
  return content ? appearanceName(send0(content, sel("appearance"))) : "";
}

const char *fd_test_app_appearance(void) {
  return appearanceName(send0(send0(cls("NSApplication"), sel("sharedApplication")), sel("appearance")));
}

void fd_test_set_app_appearance(const char *name) {
  id appearance = name && *name ? send1(cls("NSAppearance"), sel("appearanceNamed:"), nsstring(name)) : (id)0;
  sendVoid1(send0(cls("NSApplication"), sel("sharedApplication")), sel("setAppearance:"), appearance);
}

double fd_test_capsule_slot_background_brightness(int i) {
  id wrapper = slot(i, NULL);
  if (!wrapper) return -1;
  id layer = send0(wrapper, sel("layer"));
  if (!layer) return -1;
  CGColorRef colour = ((CGColorRef(*)(id, SEL))objc_msgSend)(layer, sel("backgroundColor"));
  if (!colour) return -1;
  size_t n = CGColorGetNumberOfComponents(colour);
  const CGFloat *c = CGColorGetComponents(colour);
  return n >= 3 ? (c[0] + c[1] + c[2]) / 3 : n >= 1 ? c[0] : -1;
}
