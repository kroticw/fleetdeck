// The orchestrator and sessions surfaces: WKWebViews the window makes itself,
// through the Objective-C runtime from C, as webview_go makes the board's
// (libs/webview/include/webview.h, set_up_web_view). This file is not built
// under ARC, so everything it allocates it releases, in fd_surface_destroy.
#include "surface_darwin.h"

#include "_cgo_export.h"
#include "nav_darwin.h"

#include <CoreGraphics/CGGeometry.h>
#include <objc/message.h>
#include <objc/objc.h>
#include <objc/runtime.h>
#include <stdlib.h>

// Declared by the runtime and not in a public header; webview.h's
// objc::autoreleasepool is the same pair.
extern void *objc_autoreleasePoolPush(void);
extern void objc_autoreleasePoolPop(void *pool);

static id cls(const char *name) { return (id)objc_getClass(name); }
static SEL sel(const char *name) { return sel_registerName(name); }

static id send0(id recv, SEL s) { return ((id (*)(id, SEL))objc_msgSend)(recv, s); }
static id send1(id recv, SEL s, id a) { return ((id (*)(id, SEL, id))objc_msgSend)(recv, s, a); }
static void sendVoid0(id recv, SEL s) { ((void (*)(id, SEL))objc_msgSend)(recv, s); }
static void sendVoid1(id recv, SEL s, id a) { ((void (*)(id, SEL, id))objc_msgSend)(recv, s, a); }
static void sendVoid2(id recv, SEL s, id a, id b) { ((void (*)(id, SEL, id, id))objc_msgSend)(recv, s, a, b); }
static void sendVoidLong(id recv, SEL s, long v) { ((void (*)(id, SEL, long))objc_msgSend)(recv, s, v); }
static long sendLong0(id recv, SEL s) { return ((long (*)(id, SEL))objc_msgSend)(recv, s); }
static signed char sendBool0(id recv, SEL s) { return ((signed char (*)(id, SEL))objc_msgSend)(recv, s); }

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
  return ((id (*)(id, SEL, const char *))objc_msgSend)(cls("NSString"), sel("stringWithUTF8String:"), s);
}

static const char *cstring(id s) {
  return s ? ((const char *(*)(id, SEL))objc_msgSend)(s, sel("UTF8String")) : "";
}

static id nsbool(int v) {
  return ((id (*)(id, SEL, signed char))objc_msgSend)(cls("NSNumber"), sel("numberWithBool:"), (signed char)(v != 0));
}

// The surface's name, on the handler and the delegate, for the Go callbacks:
// kept the way the navigation reports read it (nav_darwin.c).
static const char *nameOf(id self) { return fleetdeck_web_view_name(self); }

static void setName(id object, const char *name) { fleetdeck_set_web_view_name(object, name); }

// --- the message handler -------------------------------------------------------

static int liveHandlers;
static int handlerClassRegistrations;
static Class handlerSuper;

static void handlerReceive(id self, SEL _cmd, id controller, id message) {
  (void)_cmd;
  (void)controller;
  id body = send0(message, sel("body"));
  // The host script posts JSON text; anything else is not a call.
  if (!body || !((signed char (*)(id, SEL, id))objc_msgSend)(body, sel("isKindOfClass:"), cls("NSString"))) return;
  fleetdeckSurfaceMessage((char *)nameOf(self), (char *)cstring(body));
}

static void handlerDealloc(id self, SEL _cmd) {
  liveHandlers--;
  struct objc_super up = {self, handlerSuper};
  ((void (*)(struct objc_super *, SEL))objc_msgSendSuper)(&up, _cmd);
}

static Class handlerClass(void) {
  static Class klass;
  if (klass) return klass;
  handlerSuper = (Class)objc_getClass("NSObject");
  klass = objc_allocateClassPair(handlerSuper, "FleetdeckSurfaceHandler", 0);
  class_addProtocol(klass, objc_getProtocol("WKScriptMessageHandler"));
  class_addMethod(klass, sel("userContentController:didReceiveScriptMessage:"), (IMP)handlerReceive, "v@:@@");
  class_addMethod(klass, sel("dealloc"), (IMP)handlerDealloc, "v@:");
  objc_registerClassPair(klass);
  handlerClassRegistrations++;
  return klass;
}

// --- the navigation delegate ---------------------------------------------------

// The layout of a block (clang's Block ABI): the invoke function is the fourth
// field, and takes the block itself first.
struct fd_block {
  void *isa;
  int flags;
  int reserved;
  void (*invoke)(void);
};

static const char *urlOf(id action) {
  return cstring(send0(send0(send0(action, sel("request")), sel("URL")), sel("absoluteString")));
}

static void navigationDecide(id self, SEL _cmd, id webView, id action, id decisionHandler) {
  (void)_cmd;
  (void)webView;
  int allow = fleetdeckSurfaceNavigation((char *)nameOf(self), (char *)urlOf(action));
  void (*invoke)(id, long) = (void (*)(id, long))((struct fd_block *)decisionHandler)->invoke;
  invoke(decisionHandler, allow ? 1 : 0);  // WKNavigationActionPolicyAllow is 1, Cancel 0
}

// A link to a new window: the same rules, and never a second web view.
static id navigationNewWindow(id self, SEL _cmd, id webView, id configuration, id action, id features) {
  (void)_cmd;
  (void)webView;
  (void)configuration;
  (void)features;
  fleetdeckSurfaceNavigation((char *)nameOf(self), (char *)urlOf(action));
  return (id)0;
}

static Class navigatorClass(void) {
  static Class klass;
  if (klass) return klass;
  klass = objc_allocateClassPair((Class)objc_getClass("NSObject"), "FleetdeckSurfaceNavigation", 0);
  class_addProtocol(klass, objc_getProtocol("WKNavigationDelegate"));
  class_addProtocol(klass, objc_getProtocol("WKUIDelegate"));
  class_addMethod(klass, sel("webView:decidePolicyForNavigationAction:decisionHandler:"), (IMP)navigationDecide,
                  "v@:@@@?");
  class_addMethod(klass, sel("webView:createWebViewWithConfiguration:forNavigationAction:windowFeatures:"),
                  (IMP)navigationNewWindow, "@@:@@@@");
  // What WebKit says of each navigation, as the board's delegate says it: with a
  // delegate set, WebKit no longer reloads a page whose process went away, and
  // the window does (controller.go).
  fleetdeck_navigation_reporting(klass);
  objc_registerClassPair(klass);
  return klass;
}

// --- the surface ----------------------------------------------------------------

struct fd_surface {
  id webview;
  id config;
  id handler;
  id navigator;
};

void *fd_surface_create(void *board, void *container, const char *name, const char **scripts, int count) {
  void *pool = objc_autoreleasePoolPush();
  struct fd_surface *s = calloc(1, sizeof *s);

  s->config = send0(cls("WKWebViewConfiguration"), sel("new"));
  // One process and one store with the board: a surface on a store of its own
  // would have a localStorage of its own, and the theme would split.
  id boardConfig = send0((id)board, sel("configuration"));
  sendVoid1(s->config, sel("setProcessPool:"), send0(boardConfig, sel("processPool")));
  sendVoid1(s->config, sel("setWebsiteDataStore:"), send0(boardConfig, sel("websiteDataStore")));
  // What webview.h turns on for the board: paste into a terminal needs them.
  id prefs = send0(s->config, sel("preferences"));
  for (const char **key = (const char *[]){"fullScreenEnabled", "javaScriptCanAccessClipboard", "DOMPasteAllowed", 0};
       *key; key++) {
    sendVoid2(prefs, sel("setValue:forKey:"), nsbool(1), nsstring(*key));
  }

  id content = send0(s->config, sel("userContentController"));
  for (int i = 0; i < count; i++) {
    id script = ((id (*)(id, SEL, id, long, signed char))objc_msgSend)(
        send0(cls("WKUserScript"), sel("alloc")), sel("initWithSource:injectionTime:forMainFrameOnly:"),
        nsstring(scripts[i]), 0 /* at document start */, 1);
    sendVoid1(content, sel("addUserScript:"), script);
    sendVoid0(script, sel("release"));
  }

  s->handler = send0((id)handlerClass(), sel("new"));
  liveHandlers++;
  setName(s->handler, name);
  sendVoid2(content, sel("addScriptMessageHandler:name:"), s->handler, nsstring("fleetdeck"));

  s->navigator = send0((id)navigatorClass(), sel("new"));
  setName(s->navigator, name);

  CGRect bounds = sendRect0((id)container, sel("bounds"));
  s->webview = ((id (*)(id, SEL, CGRect, id))objc_msgSend)(send0(cls("WKWebView"), sel("alloc")),
                                                           sel("initWithFrame:configuration:"), bounds, s->config);
  // Transparent, so the glass under the web view is what shows.
  sendVoid2(s->webview, sel("setValue:forKey:"), nsbool(0), nsstring("drawsBackground"));
  sendVoid1(s->webview, sel("setUnderPageBackgroundColor:"), send0(cls("NSColor"), sel("clearColor")));
  sendVoid1(s->webview, sel("setNavigationDelegate:"), s->navigator);
  sendVoid1(s->webview, sel("setUIDelegate:"), s->navigator);
  sendVoidLong(s->webview, sel("setAutoresizingMask:"), 18);
  sendVoid1((id)container, sel("addSubview:"), s->webview);

  objc_autoreleasePoolPop(pool);
  return s;
}

void fd_surface_load(void *surface, const char *url) {
  void *pool = objc_autoreleasePoolPush();
  struct fd_surface *s = surface;
  id request = send1(cls("NSURLRequest"), sel("requestWithURL:"), send1(cls("NSURL"), sel("URLWithString:"), nsstring(url)));
  sendVoid1(s->webview, sel("loadRequest:"), request);
  objc_autoreleasePoolPop(pool);
}

void fd_surface_eval(void *surface, const char *js) {
  void *pool = objc_autoreleasePoolPush();
  struct fd_surface *s = surface;
  sendVoid2(s->webview, sel("evaluateJavaScript:completionHandler:"), nsstring(js), (id)0);
  objc_autoreleasePoolPop(pool);
}

void fd_surface_focus(void *surface) {
  struct fd_surface *s = surface;
  id window = send0(s->webview, sel("window"));
  if (window) sendVoid1(window, sel("makeFirstResponder:"), s->webview);
}

void fd_surface_destroy(void *surface) {
  void *pool = objc_autoreleasePoolPush();
  struct fd_surface *s = surface;
  sendVoid0(s->webview, sel("stopLoading"));
  sendVoid1(s->webview, sel("setNavigationDelegate:"), (id)0);
  sendVoid1(s->webview, sel("setUIDelegate:"), (id)0);
  // The content controller holds the handler until the handler is removed.
  id content = send0(s->config, sel("userContentController"));
  sendVoid1(content, sel("removeScriptMessageHandlerForName:"), nsstring("fleetdeck"));
  sendVoid0(content, sel("removeAllUserScripts"));
  sendVoid0(s->webview, sel("removeFromSuperview"));
  sendVoid0(s->webview, sel("release"));
  sendVoid0(s->config, sel("release"));
  sendVoid0(s->handler, sel("release"));
  sendVoid0(s->navigator, sel("release"));
  free(s);
  objc_autoreleasePoolPop(pool);
}

int fd_surface_live_handlers(void) { return liveHandlers; }
int fd_surface_handler_class_registrations(void) { return handlerClassRegistrations; }

// --- for the tests ---------------------------------------------------------------

void *fd_test_board_window(double width, double height) {
  id w = send0(cls("NSWindow"), sel("alloc"));
  CGRect rect = CGRectMake(0, 0, width, height);
  unsigned long style = 1 | 2 | 4 | 8;
  w = ((id (*)(id, SEL, CGRect, unsigned long, unsigned long, signed char))objc_msgSend)(
      w, sel("initWithContentRect:styleMask:backing:defer:"), rect, style, 2, 0);
  id config = send0(cls("WKWebViewConfiguration"), sel("new"));
  id board = ((id (*)(id, SEL, CGRect, id))objc_msgSend)(send0(cls("WKWebView"), sel("alloc")),
                                                         sel("initWithFrame:configuration:"), rect, config);
  sendVoid1(w, sel("setContentView:"), board);
  return w;
}

void *fd_test_surface_webview(void *surface) { return ((struct fd_surface *)surface)->webview; }

int fd_test_reports_navigation(void *surface) {
  id navigator = ((struct fd_surface *)surface)->navigator;
  for (const char **name = (const char *[]){"webView:didCommitNavigation:", "webView:didFailProvisionalNavigation:withError:",
                                            "webViewWebContentProcessDidTerminate:", 0};
       *name; name++) {
    if (!((signed char (*)(id, SEL, SEL))objc_msgSend)(navigator, sel("respondsToSelector:"), sel(*name))) return 0;
  }
  return 1;
}

int fd_test_draws_background(void *webview) {
  return sendBool0(send1((id)webview, sel("valueForKey:"), nsstring("drawsBackground")), sel("boolValue")) != 0;
}

static id configOf(id webview) { return send0(webview, sel("configuration")); }

int fd_test_shares_pool(void *surface, void *board) {
  id mine = send0(configOf(((struct fd_surface *)surface)->webview), sel("processPool"));
  return mine == send0(configOf((id)board), sel("processPool"));
}

int fd_test_shares_store(void *surface, void *board) {
  id mine = send0(configOf(((struct fd_surface *)surface)->webview), sel("websiteDataStore"));
  return mine == send0(configOf((id)board), sel("websiteDataStore"));
}

int fd_test_user_scripts(void *surface) {
  id content = send0(((struct fd_surface *)surface)->config, sel("userContentController"));
  return (int)sendLong0(send0(content, sel("userScripts")), sel("count"));
}

int fd_test_subview_count(void *view) { return (int)sendLong0(send0((id)view, sel("subviews")), sel("count")); }
