// What the glass window needs of AppKit besides its views: the appearance, the
// browser, first responder, defaults, and word of the window changing. Raw
// runtime calls, as menu_darwin.c.
#include "window_darwin.h"

#include "_cgo_export.h"

#include <CoreGraphics/CGGeometry.h>
#include <objc/message.h>
#include <objc/objc.h>
#include <objc/runtime.h>

extern void *objc_autoreleasePoolPush(void);
extern void objc_autoreleasePoolPop(void *pool);

static id cls(const char *name) { return (id)objc_getClass(name); }
static SEL sel(const char *name) { return sel_registerName(name); }

static id send0(id recv, SEL s) { return ((id (*)(id, SEL))objc_msgSend)(recv, s); }
static id send1(id recv, SEL s, id a) { return ((id (*)(id, SEL, id))objc_msgSend)(recv, s, a); }
static void sendVoid1(id recv, SEL s, id a) { ((void (*)(id, SEL, id))objc_msgSend)(recv, s, a); }

static CGRect sendRect0(id recv, SEL s) {
#if defined(__x86_64__)
  CGRect r;
  ((void (*)(CGRect *, id, SEL))objc_msgSend_stret)(&r, recv, s);
  return r;
#else
  return ((CGRect (*)(id, SEL))objc_msgSend)(recv, s);
#endif
}

static id nsstring(const char *s) {
  return ((id (*)(id, SEL, const char *))objc_msgSend)(cls("NSString"), sel("stringWithUTF8String:"), s ? s : "");
}

static int equals(const char *a, const char *b) {
  while (*a && *a == *b) a++, b++;
  return *a == *b;
}

void fd_window_set_appearance(const char *choice) {
  void *pool = objc_autoreleasePoolPush();
  id appearance = (id)0;  // the system's
  if (equals(choice, "light")) appearance = send1(cls("NSAppearance"), sel("appearanceNamed:"), nsstring("NSAppearanceNameAqua"));
  if (equals(choice, "dark")) appearance = send1(cls("NSAppearance"), sel("appearanceNamed:"), nsstring("NSAppearanceNameDarkAqua"));
  sendVoid1(send0(cls("NSApplication"), sel("sharedApplication")), sel("setAppearance:"), appearance);
  objc_autoreleasePoolPop(pool);
}

void fd_open_external(const char *url) {
  void *pool = objc_autoreleasePoolPush();
  id u = send1(cls("NSURL"), sel("URLWithString:"), nsstring(url));
  if (u) {
    ((signed char (*)(id, SEL, id))objc_msgSend)(send0(cls("NSWorkspace"), sel("sharedWorkspace")), sel("openURL:"), u);
  }
  objc_autoreleasePoolPop(pool);
}

void fd_focus_view(void *view) {
  id window = send0((id)view, sel("window"));
  if (window) sendVoid1(window, sel("makeFirstResponder:"), (id)view);
}

static id defaults(void) { return send0(cls("NSUserDefaults"), sel("standardUserDefaults")); }

double fd_defaults_double(const char *key, int *found) {
  void *pool = objc_autoreleasePoolPush();
  id k = nsstring(key);
  *found = send1(defaults(), sel("objectForKey:"), k) != (id)0;
  double v = ((double (*)(id, SEL, id))objc_msgSend)(defaults(), sel("doubleForKey:"), k);
  objc_autoreleasePoolPop(pool);
  return v;
}

void fd_defaults_set_double(const char *key, double value) {
  void *pool = objc_autoreleasePoolPush();
  ((void (*)(id, SEL, double, id))objc_msgSend)(defaults(), sel("setDouble:forKey:"), value, nsstring(key));
  objc_autoreleasePoolPop(pool);
}

int fd_defaults_bool(const char *key) {
  void *pool = objc_autoreleasePoolPush();
  int v = ((signed char (*)(id, SEL, id))objc_msgSend)(defaults(), sel("boolForKey:"), nsstring(key)) != 0;
  objc_autoreleasePoolPop(pool);
  return v;
}

void fd_defaults_set_bool(const char *key, int value) {
  void *pool = objc_autoreleasePoolPush();
  ((void (*)(id, SEL, signed char, id))objc_msgSend)(defaults(), sel("setBool:forKey:"), (signed char)(value != 0),
                                                     nsstring(key));
  objc_autoreleasePoolPop(pool);
}

static void changedSize(id self, SEL _cmd, id note) {
  (void)self, (void)_cmd, (void)note;
  fleetdeckWindowChanged((char *)"size");
}

static void changedFullScreen(id self, SEL _cmd, id note) {
  (void)self, (void)_cmd, (void)note;
  fleetdeckWindowChanged((char *)"fullscreen");
}

static void changedGlass(id self, SEL _cmd, id note) {
  (void)self, (void)_cmd, (void)note;
  fleetdeckWindowChanged((char *)"glass");
}

static void observe(id center, id observer, const char *selector, const char *name, id object) {
  ((void (*)(id, SEL, id, SEL, id, id))objc_msgSend)(center, sel("addObserver:selector:name:object:"), observer,
                                                     sel(selector), nsstring(name), object);
}

void fd_observe_window(void *window) {
  void *pool = objc_autoreleasePoolPush();
  Class klass = (Class)objc_getClass("FleetdeckWindowObserver");
  if (!klass) {
    klass = objc_allocateClassPair((Class)objc_getClass("NSObject"), "FleetdeckWindowObserver", 0);
    class_addMethod(klass, sel("changedSize:"), (IMP)changedSize, "v@:@");
    class_addMethod(klass, sel("changedFullScreen:"), (IMP)changedFullScreen, "v@:@");
    class_addMethod(klass, sel("changedGlass:"), (IMP)changedGlass, "v@:@");
    objc_registerClassPair(klass);
  }
  // Kept for the life of the process, like the window it watches.
  id observer = send0((id)klass, sel("new"));
  id center = send0(cls("NSNotificationCenter"), sel("defaultCenter"));
  observe(center, observer, "changedSize:", "NSWindowDidResizeNotification", (id)window);
  observe(center, observer, "changedFullScreen:", "NSWindowDidEnterFullScreenNotification", (id)window);
  observe(center, observer, "changedFullScreen:", "NSWindowDidExitFullScreenNotification", (id)window);
  id workspaceCenter = send0(send0(cls("NSWorkspace"), sel("sharedWorkspace")), sel("notificationCenter"));
  observe(workspaceCenter, observer, "changedGlass:", "NSWorkspaceAccessibilityDisplayOptionsDidChangeNotification",
          (id)0);
  objc_autoreleasePoolPop(pool);
}

void fd_window_content_size(void *window, double *width, double *height) {
  CGRect r = sendRect0(send0((id)window, sel("contentView")), sel("bounds"));
  *width = r.size.width;
  *height = r.size.height;
}

int fd_window_is_fullscreen(void *window) {
  unsigned long mask = ((unsigned long (*)(id, SEL))objc_msgSend)((id)window, sel("styleMask"));
  return (mask & (1UL << 14)) != 0;  // NSWindowStyleMaskFullScreen
}
