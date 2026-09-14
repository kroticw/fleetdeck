// The glass frame over the board: raw Objective-C runtime calls, no .m file,
// the same technique as menu_darwin.c. AppKit classes are looked up by name, so
// a system without NSGlassEffectView still builds and runs; and nothing here
// names NSGlassEffectViewStyle (Wails #4541: declaring it breaks the build on
// SDKs that define it).
//
// A panel is two views: the material (glass, vibrancy or nothing) and, over it
// with the same frame, the view the surface's web view goes into. Glass does
// not pass clicks into a content view it is given, so the web view is not its
// content.
//
// There is no NSGlassEffectContainerView. A container covering the window sits
// over the whole board, and a view over the board takes its clicks: hit-testing
// stops at the deepest view under the pointer, whether or not it handles the
// click. The panels and the capsule row sit directly above the board instead,
// and the frame's own views pass a click on to the board where they have no
// subview under it.
#include "frame_darwin.h"

#include "_cgo_export.h"

#include <CoreGraphics/CGGeometry.h>
#include <objc/message.h>
#include <objc/objc.h>
#include <objc/runtime.h>
#include <stdlib.h>
#include <string.h>

static id cls(const char *name) { return (id)objc_getClass(name); }
static SEL sel(const char *name) { return sel_registerName(name); }

static id send0(id recv, SEL s) { return ((id (*)(id, SEL))objc_msgSend)(recv, s); }
static void sendVoid0(id recv, SEL s) { ((void (*)(id, SEL))objc_msgSend)(recv, s); }
static void sendVoid1(id recv, SEL s, id arg) { ((void (*)(id, SEL, id))objc_msgSend)(recv, s, arg); }
static void sendVoidLong(id recv, SEL s, long v) { ((void (*)(id, SEL, long))objc_msgSend)(recv, s, v); }
static void sendVoidDouble(id recv, SEL s, double v) { ((void (*)(id, SEL, double))objc_msgSend)(recv, s, v); }
static void sendVoidBool(id recv, SEL s, signed char v) { ((void (*)(id, SEL, signed char))objc_msgSend)(recv, s, v); }
static void sendVoidRect(id recv, SEL s, CGRect r) { ((void (*)(id, SEL, CGRect))objc_msgSend)(recv, s, r); }
static long sendLong0(id recv, SEL s) { return ((long (*)(id, SEL))objc_msgSend)(recv, s); }
static double sendDouble0(id recv, SEL s) { return ((double (*)(id, SEL))objc_msgSend)(recv, s); }
static signed char sendBool0(id recv, SEL s) { return ((signed char (*)(id, SEL))objc_msgSend)(recv, s); }

// A CGRect is returned in memory on x86_64, where objc_msgSend_stret has to be
// used; arm64 returns it in registers through plain objc_msgSend.
static CGRect sendRect0(id recv, SEL s) {
#if defined(__x86_64__)
  CGRect r;
  ((void (*)(CGRect *, id, SEL))objc_msgSend_stret)(&r, recv, s);
  return r;
#else
  return ((CGRect (*)(id, SEL))objc_msgSend)(recv, s);
#endif
}

static id initWithFrame(const char *className, Class klass, CGRect r) {
  id obj = send0(klass ? (id)klass : cls(className), sel("alloc"));
  return ((id (*)(id, SEL, CGRect))objc_msgSend)(obj, sel("initWithFrame:"), r);
}

static CGRect cgrect(fd_rect r) { return CGRectMake(r.x, r.y, r.w, r.h); }

// Asked of the object rather than read off its class name: AppKit swaps an
// observed view's class for an NSKVONotifying_ subclass, and the glass view is
// observed.
static int isGlass(id view) {
  id glass = cls("NSGlassEffectView");
  return glass && ((signed char (*)(id, SEL, id))objc_msgSend)(view, sel("isKindOfClass:"), glass) != 0;
}

// --- the frame's own view class ---------------------------------------------
//
// Flipped, so rectangles go from the top left as the page's do; and passing a
// click through where no subview of its own is under the pointer.

static Class viewSuper;

static signed char frameIsFlipped(id self, SEL _cmd) {
  (void)self;
  (void)_cmd;
  return 1;
}

static id frameHitTest(id self, SEL _cmd, CGPoint point) {
  struct objc_super up = {self, viewSuper};
  id hit = ((id (*)(struct objc_super *, SEL, CGPoint))objc_msgSendSuper)(&up, _cmd, point);
  return hit == self ? (id)0 : hit;
}

static Class frameViewClass(void) {
  static Class klass;
  if (klass) return klass;
  klass = (Class)objc_getClass("FleetdeckFrameView");
  if (klass) return klass;
  viewSuper = (Class)objc_getClass("NSView");
  klass = objc_allocateClassPair(viewSuper, "FleetdeckFrameView", 0);
  class_addMethod(klass, sel("isFlipped"), (IMP)frameIsFlipped, "c@:");
  class_addMethod(klass, sel("hitTest:"), (IMP)frameHitTest, "@@:{CGPoint=dd}");
  objc_registerClassPair(klass);
  return klass;
}

static id frameView(CGRect r) {
  id v = initWithFrame(NULL, frameViewClass(), r);
  sendVoidLong(v, sel("setAutoresizingMask:"), 18);  // width and height sizable
  return v;
}

// --- the width strip ----------------------------------------------------------
//
// A panel's width is dragged at its inner edge, by the window: the web view in
// the panel fills it, and the geometry is the native layer's (spec 5.3). The
// strip sits over the glass and the surface's web view, and reports a press, a
// drag and a release in the window's coordinates.

static const double stripWidth = 8;

static const char *sideOf(id strip) {
  id identifier = send0(strip, sel("identifier"));
  return identifier ? ((const char *(*)(id, SEL))objc_msgSend)(identifier, sel("UTF8String")) : "";
}

static double pointerX(id event) {
  return ((CGPoint (*)(id, SEL))objc_msgSend)(event, sel("locationInWindow")).x;
}

static void stripDown(id self, SEL _cmd, id event) {
  (void)_cmd;
  fleetdeckResize((char *)sideOf(self), 0, pointerX(event));
}

static void stripDragged(id self, SEL _cmd, id event) {
  (void)_cmd;
  fleetdeckResize((char *)sideOf(self), 1, pointerX(event));
}

static void stripUp(id self, SEL _cmd, id event) {
  (void)_cmd;
  fleetdeckResize((char *)sideOf(self), 2, pointerX(event));
}

static void stripCursorRects(id self, SEL _cmd) {
  (void)_cmd;
  id cursor = send0(cls("NSCursor"), sel("resizeLeftRightCursor"));
  ((void (*)(id, SEL, CGRect, id))objc_msgSend)(self, sel("addCursorRect:cursor:"), sendRect0(self, sel("bounds")), cursor);
}

// A press on the edge of an inactive window drags at once, as a window's own
// edge does.
static signed char stripFirstMouse(id self, SEL _cmd, id event) {
  (void)self;
  (void)_cmd;
  (void)event;
  return 1;
}

static Class stripClass(void) {
  static Class klass;
  if (klass) return klass;
  klass = objc_allocateClassPair((Class)objc_getClass("NSView"), "FleetdeckResizeStrip", 0);
  class_addMethod(klass, sel("mouseDown:"), (IMP)stripDown, "v@:@");
  class_addMethod(klass, sel("mouseDragged:"), (IMP)stripDragged, "v@:@");
  class_addMethod(klass, sel("mouseUp:"), (IMP)stripUp, "v@:@");
  class_addMethod(klass, sel("resetCursorRects"), (IMP)stripCursorRects, "v@:");
  class_addMethod(klass, sel("acceptsFirstMouse:"), (IMP)stripFirstMouse, "c@:@");
  objc_registerClassPair(klass);
  return klass;
}

// --- the frame ---------------------------------------------------------------

struct fd_frame {
  id window;
  id root;
  id board;
  id wrappers[2];
  id contents[2];
  CGRect rects[2];
  id capsules;
  id strips[2];
  char mode[16];
};

void *fd_frame_install(void *window) {
  struct fd_frame *f = calloc(1, sizeof *f);
  f->window = (id)window;
  f->board = send0(f->window, sel("contentView"));

  // The content runs under the title bar, whose buttons float over the
  // orchestrator panel's top corner. Set before the root goes in, while the
  // board is still the content view: on the macos-15 runner of PR #166 a root
  // put in first kept the content area below the title bar when the style
  // changed, and the title bar's 28 pt above the frame stayed black. The root
  // is then made the size of the whole window, not of what the board had.
  unsigned long mask = (unsigned long)sendLong0(f->window, sel("styleMask"));
  sendVoidLong(f->window, sel("setStyleMask:"), (long)(mask | (1UL << 15)));  // full-size content view
  sendVoidBool(f->window, sel("setTitlebarAppearsTransparent:"), 1);
  sendVoidLong(f->window, sel("setTitleVisibility:"), 1);  // hidden

  CGRect whole = sendRect0(f->window, sel("frame"));
  CGRect bounds = CGRectMake(0, 0, whole.size.width, whole.size.height);
  f->root = frameView(bounds);
  sendVoid1(f->window, sel("setContentView:"), f->root);
  sendVoidRect(f->root, sel("setFrame:"), bounds);
  if (f->board) {
    sendVoidRect(f->board, sel("setFrame:"), sendRect0(f->root, sel("bounds")));
    sendVoidLong(f->board, sel("setAutoresizingMask:"), 18);
    sendVoid1(f->root, sel("addSubview:"), f->board);
  }

  for (int side = 0; side < 2; side++) {
    // Placed by fd_frame_layout, never stretched with the root; rounded like
    // the glass under it, so nothing of a surface shows past its corners.
    id content = frameView(CGRectZero);
    sendVoidLong(content, sel("setAutoresizingMask:"), 0);
    sendVoidBool(content, sel("setWantsLayer:"), 1);
    id layer = send0(content, sel("layer"));
    sendVoidDouble(layer, sel("setCornerRadius:"), 18.0);
    sendVoidBool(layer, sel("setMasksToBounds:"), 1);
    f->contents[side] = content;
  }
  f->capsules = initWithFrame(NULL, frameViewClass(), CGRectZero);
  sendVoid1(f->root, sel("addSubview:"), f->capsules);
  // Above everything else in the frame, over the panels' edges.
  const char *sides[2] = {"orchestrator", "sessions"};
  for (int side = 0; side < 2; side++) {
    f->strips[side] = initWithFrame(NULL, stripClass(), CGRectZero);
    sendVoid1(f->strips[side], sel("setIdentifier:"),
              ((id (*)(id, SEL, const char *))objc_msgSend)(cls("NSString"), sel("stringWithUTF8String:"), sides[side]));
    sendVoidBool(f->strips[side], sel("setHidden:"), 1);
    sendVoid1(f->root, sel("addSubview:"), f->strips[side]);
  }
  return f;
}

static id wrapperFor(const char *mode, CGRect r) {
  if (strcmp(mode, "glass") == 0 && cls("NSGlassEffectView")) {
    id g = initWithFrame("NSGlassEffectView", Nil, r);
    sendVoidLong(g, sel("setStyle:"), 0);  // Regular; Clear makes the text under it unreadable
    sendVoidDouble(g, sel("setCornerRadius:"), 18.0);
    return g;
  }
  id w;
  if (strcmp(mode, "opaque") == 0) {
    w = initWithFrame("NSView", Nil, r);
  } else {
    w = initWithFrame("NSVisualEffectView", Nil, r);
    sendVoidLong(w, sel("setBlendingMode:"), 1);  // within the window: blurs the board
    sendVoidLong(w, sel("setMaterial:"), 7);      // sidebar
    sendVoidLong(w, sel("setState:"), 1);         // active
  }
  sendVoidBool(w, sel("setWantsLayer:"), 1);
  id layer = send0(w, sel("layer"));
  sendVoidDouble(layer, sel("setCornerRadius:"), 18.0);
  sendVoidBool(layer, sel("setMasksToBounds:"), 1);
  return w;
}

void fd_frame_set_mode(void *frame, const char *mode) {
  struct fd_frame *f = frame;
  strncpy(f->mode, mode, sizeof f->mode - 1);
  for (int side = 0; side < 2; side++) {
    id content = f->contents[side];
    if (f->wrappers[side]) {
      sendVoid0(content, sel("removeFromSuperview"));
      sendVoid0(f->wrappers[side], sel("removeFromSuperview"));
      sendVoid0(f->wrappers[side], sel("release"));
    }
    id w = wrapperFor(f->mode, f->rects[side]);
    f->wrappers[side] = w;
    // Above the board and below the capsule row.
    ((void (*)(id, SEL, id, long, id))objc_msgSend)(f->root, sel("addSubview:positioned:relativeTo:"), w, -1,
                                                    f->capsules);
    // The view a surface goes into sits over its panel, not inside it. Glass
    // keeps the clicks on its own content view: a web view given to it as
    // content is drawn but cannot be clicked (found by the surface test). Over
    // the glass, the transparent web view still shows the glass, and the glass
    // still shows the board under it.
    sendVoidRect(content, sel("setFrame:"), f->rects[side]);
    ((void (*)(id, SEL, id, long, id))objc_msgSend)(f->root, sel("addSubview:positioned:relativeTo:"), content, 1, w);
  }
}

void fd_frame_layout(void *frame, fd_rect orchestrator, fd_rect sessions, fd_rect capsules, int orchestratorResizable,
                     int sessionsResizable) {
  struct fd_frame *f = frame;
  f->rects[0] = cgrect(orchestrator);
  f->rects[1] = cgrect(sessions);
  for (int side = 0; side < 2; side++) {
    if (!f->wrappers[side]) continue;
    sendVoidRect(f->wrappers[side], sel("setFrame:"), f->rects[side]);
    sendVoidRect(f->contents[side], sel("setFrame:"), f->rects[side]);
  }
  sendVoidRect(f->capsules, sel("setFrame:"), cgrect(capsules));

  // The orchestrator's inner edge is its right one, the sessions panel's its left.
  double edges[2] = {orchestrator.x + orchestrator.w, sessions.x};
  int resizable[2] = {orchestratorResizable, sessionsResizable};
  for (int side = 0; side < 2; side++) {
    CGRect r = f->rects[side];
    sendVoidRect(f->strips[side], sel("setFrame:"),
                 CGRectMake(edges[side] - stripWidth / 2, r.origin.y, stripWidth, r.size.height));
    sendVoidBool(f->strips[side], sel("setHidden:"), !(resizable[side] && r.size.width > 0));
    id window = send0(f->strips[side], sel("window"));
    if (window) sendVoid1(window, sel("invalidateCursorRectsForView:"), f->strips[side]);
  }
}

void *fd_frame_panel_content(void *frame, int side) { return ((struct fd_frame *)frame)->contents[side]; }
void *fd_frame_capsules(void *frame) { return ((struct fd_frame *)frame)->capsules; }
void *fd_frame_board(void *frame) { return ((struct fd_frame *)frame)->board; }

int fd_glass_available(void) { return cls("NSGlassEffectView") != (id)0; }

static id workspace(void) { return send0(cls("NSWorkspace"), sel("sharedWorkspace")); }

int fd_reduce_transparency(void) {
  return sendBool0(workspace(), sel("accessibilityDisplayShouldReduceTransparency")) != 0;
}

int fd_increase_contrast(void) {
  return sendBool0(workspace(), sel("accessibilityDisplayShouldIncreaseContrast")) != 0;
}

// --- for the tests -----------------------------------------------------------

void *fd_test_window(double width, double height) {
  id w = send0(cls("NSWindow"), sel("alloc"));
  CGRect rect = CGRectMake(0, 0, width, height);
  unsigned long style = 1 | 2 | 4 | 8;  // titled, closable, miniaturizable, resizable
  w = ((id (*)(id, SEL, CGRect, unsigned long, unsigned long, signed char))objc_msgSend)(
      w, sel("initWithContentRect:styleMask:backing:defer:"), rect, style, 2, 0);
  // A plain view stands in for the board's web view.
  sendVoid1(w, sel("setContentView:"), initWithFrame("NSView", Nil, rect));
  return w;
}

fd_rect fd_test_window_frame(void *window) {
  CGRect r = sendRect0((id)window, sel("frame"));
  return (fd_rect){r.origin.x, r.origin.y, r.size.width, r.size.height};
}

void *fd_test_panel(void *frame, int side) { return ((struct fd_frame *)frame)->wrappers[side]; }
void *fd_test_root(void *frame) { return ((struct fd_frame *)frame)->root; }
// -class, which answers the class the view was made as, not AppKit's KVO subclass.
const char *fd_test_class_name(void *view) { return view ? class_getName((Class)send0((id)view, sel("class"))) : ""; }
long fd_test_glass_style(void *view) { return sendLong0((id)view, sel("style")); }

double fd_test_corner_radius(void *view) {
  if (isGlass((id)view)) {
    return sendDouble0((id)view, sel("cornerRadius"));
  }
  return sendDouble0(send0((id)view, sel("layer")), sel("cornerRadius"));
}

long fd_test_blending_mode(void *view) { return sendLong0((id)view, sel("blendingMode")); }
long fd_test_material(void *view) { return sendLong0((id)view, sel("material")); }

int fd_test_subview_index(void *parent, void *child) {
  id subviews = send0((id)parent, sel("subviews"));
  long n = sendLong0(subviews, sel("count"));
  for (long i = 0; i < n; i++) {
    if (((id (*)(id, SEL, unsigned long))objc_msgSend)(subviews, sel("objectAtIndex:"), (unsigned long)i) ==
        (id)child) {
      return (int)i;
    }
  }
  return -1;
}

fd_rect fd_test_frame_of(void *view) {
  CGRect r = sendRect0((id)view, sel("frame"));
  fd_rect out = {r.origin.x, r.origin.y, r.size.width, r.size.height};
  return out;
}

void *fd_test_strip(void *frame, int side) { return ((struct fd_frame *)frame)->strips[side]; }

int fd_test_is_hidden(void *view) { return sendBool0((id)view, sel("isHidden")) != 0; }

// A press, a drag or a release at x in the window, sent to view as AppKit
// sends one: through the view's own mouse methods.
void fd_test_mouse(void *view, int phase, double x) {
  unsigned long types[3] = {1, 6, 2};  // left mouse down, dragged, up
  id window = send0((id)view, sel("window"));
  long number = window ? sendLong0(window, sel("windowNumber")) : 0;
  id event = ((id (*)(id, SEL, unsigned long, CGPoint, unsigned long, double, long, id, long, long, float))objc_msgSend)(
      cls("NSEvent"), sel("mouseEventWithType:location:modifierFlags:timestamp:windowNumber:context:eventNumber:clickCount:pressure:"),
      types[phase], CGPointMake(x, 100), 0, 0, number, (id)0, 0, 1, 1.0f);
  const char *selectors[3] = {"mouseDown:", "mouseDragged:", "mouseUp:"};
  sendVoid1((id)view, sel(selectors[phase]), event);
}

// Whether a click at (x, y), in the frame's coordinates from the top left,
// lands on view or inside it.
int fd_test_hit_within(void *frame, double x, double y, void *view) {
  struct fd_frame *f = frame;
  id superview = send0(f->root, sel("superview"));
  CGPoint p = CGPointMake(x, y);
  if (superview) {
    p = ((CGPoint (*)(id, SEL, CGPoint, id))objc_msgSend)(f->root, sel("convertPoint:toView:"), p, superview);
  }
  id hit = ((id (*)(id, SEL, CGPoint))objc_msgSend)(f->root, sel("hitTest:"), p);
  return hit && ((signed char (*)(id, SEL, id))objc_msgSend)(hit, sel("isDescendantOf:"), (id)view) != 0;
}

// What a click at (x, y) lands on, and the views above it up to the root, as
// class names: "WKWebView < FleetdeckFrameView < NSGlassEffectView".
const char *fd_test_hit_chain(void *frame, double x, double y) {
  static char chain[512];
  struct fd_frame *f = frame;
  id superview = send0(f->root, sel("superview"));
  CGPoint p = CGPointMake(x, y);
  if (superview) {
    p = ((CGPoint (*)(id, SEL, CGPoint, id))objc_msgSend)(f->root, sel("convertPoint:toView:"), p, superview);
  }
  id hit = ((id (*)(id, SEL, CGPoint))objc_msgSend)(f->root, sel("hitTest:"), p);
  chain[0] = 0;
  if (!hit) return "nothing";
  for (id v = hit; v && v != f->root; v = send0(v, sel("superview"))) {
    if (chain[0]) strncat(chain, " < ", sizeof chain - strlen(chain) - 1);
    strncat(chain, class_getName((Class)send0(v, sel("class"))), sizeof chain - strlen(chain) - 1);
  }
  return chain;
}

int fd_test_passes_through(void *view) {
  CGRect r = sendRect0((id)view, sel("frame"));
  CGPoint centre = CGPointMake(r.origin.x + r.size.width / 2, r.origin.y + r.size.height / 2);
  id hit = ((id (*)(id, SEL, CGPoint))objc_msgSend)((id)view, sel("hitTest:"), centre);
  return hit == (id)0;
}
