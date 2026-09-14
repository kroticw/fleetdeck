// A WKNavigationDelegate built with raw Objective-C runtime calls, the way
// menu_darwin.c builds its delegates. It only reports: what WKWebView says of
// each navigation -- begun, committed, finished, failed before or after it
// committed -- and a web content process that went away. Deciding anything
// from it is the Go side's business (nav_darwin.go).
#include "nav_darwin.h"

#include <objc/message.h>
#include <objc/objc.h>
#include <objc/runtime.h>
#include <stdint.h>

#include "_cgo_export.h"

// Kinds, as nav_darwin.go numbers them.
enum {
  kindStarted = 1,
  kindCommitted = 2,
  kindFinished = 3,
  kindFailedProvisional = 4,
  kindFailed = 5,
  kindProcessGone = 6,
};

static SEL sel(const char *name) { return sel_registerName(name); }

static id send0(id recv, SEL s) {
  return ((id (*)(id, SEL))objc_msgSend)(recv, s);
}

static const char *utf8(id nsstring) {
  if (!nsstring) {
    return "";
  }
  const char *s = ((const char *(*)(id, SEL))objc_msgSend)(nsstring, sel("UTF8String"));
  return s ? s : "";
}

// The web content process WKWebView runs the page in: a private property, asked
// only when WKWebView answers to it, and 0 otherwise.
static int webProcess(id webView) {
  SEL s = sel("_webProcessIdentifier");
  if (!((BOOL (*)(id, SEL, SEL))objc_msgSend)(webView, sel("respondsToSelector:"), s)) {
    return 0;
  }
  return ((int (*)(id, SEL))objc_msgSend)(webView, s);
}

static void report(int kind, id webView, id navigation, id error) {
  id url = send0(webView, sel("URL"));
  const char *href = url ? utf8(send0(url, sel("absoluteString"))) : "";
  long code = 0;
  const char *domain = "";
  if (error) {
    code = ((long (*)(id, SEL))objc_msgSend)(error, sel("code"));
    domain = utf8(send0(error, sel("domain")));
  }
  fleetdeckNavigationEvent(kind, (uintptr_t)navigation, (char *)href, code, (char *)domain,
                           webProcess(webView));
}

static void didStart(id self, SEL _cmd, id webView, id navigation) {
  report(kindStarted, webView, navigation, nil);
}
static void didCommit(id self, SEL _cmd, id webView, id navigation) {
  report(kindCommitted, webView, navigation, nil);
}
static void didFinish(id self, SEL _cmd, id webView, id navigation) {
  report(kindFinished, webView, navigation, nil);
}
static void didFailProvisional(id self, SEL _cmd, id webView, id navigation, id error) {
  report(kindFailedProvisional, webView, navigation, error);
}
static void didFail(id self, SEL _cmd, id webView, id navigation, id error) {
  report(kindFailed, webView, navigation, error);
}
static void processGone(id self, SEL _cmd, id webView) {
  report(kindProcessGone, webView, nil, nil);
}

// The delegate is kept here for the life of the process: WKWebView holds its
// navigation delegate weakly.
static id delegate;

int fleetdeck_observe_navigation(void *window) {
  // webview_go puts its WKWebView as the window's content view; a later
  // webview_go that does not is told apart here rather than sent messages
  // only a WKWebView answers.
  id webView = send0((id)window, sel("contentView"));
  Class wk = objc_getClass("WKWebView");
  if (!webView || !wk ||
      !((BOOL (*)(id, SEL, Class))objc_msgSend)(webView, sel("isKindOfClass:"), wk)) {
    return 0;
  }
  const char *name = "FleetdeckNavigationDelegate";
  Class c = objc_getClass(name);
  if (!c) {
    c = objc_allocateClassPair(objc_getClass("NSObject"), name, 0);
    class_addProtocol(c, objc_getProtocol("WKNavigationDelegate"));
    class_addMethod(c, sel("webView:didStartProvisionalNavigation:"), (IMP)didStart, "v@:@@");
    class_addMethod(c, sel("webView:didCommitNavigation:"), (IMP)didCommit, "v@:@@");
    class_addMethod(c, sel("webView:didFinishNavigation:"), (IMP)didFinish, "v@:@@");
    class_addMethod(c, sel("webView:didFailProvisionalNavigation:withError:"),
                    (IMP)didFailProvisional, "v@:@@@");
    class_addMethod(c, sel("webView:didFailNavigation:withError:"), (IMP)didFail, "v@:@@@");
    class_addMethod(c, sel("webViewWebContentProcessDidTerminate:"), (IMP)processGone, "v@:@");
    objc_registerClassPair(c);
  }
  delegate = send0(send0((id)c, sel("alloc")), sel("init"));
  ((void (*)(id, SEL, id))objc_msgSend)(webView, sel("setNavigationDelegate:"), delegate);
  return 1;
}
