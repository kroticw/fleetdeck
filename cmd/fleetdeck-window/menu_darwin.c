// Raw Objective-C runtime calls, no .m file and no Objective-C compiler mode
// -- the same technique webview_go's own Cocoa backend uses (see
// libs/webview/include/webview.h), kept here for the one thing that library
// does not do: build a menu bar. Without it, Cocoa has no key equivalent to
// route Cmd+X/C/V/A/Z through, even though WKWebView implements every one of
// those commands once reached (confirmed live: the right-click context
// menu's Paste already works today).
#include "menu_darwin.h"

#include <objc/message.h>
#include <objc/objc.h>
#include <objc/runtime.h>
#include <stdbool.h>

static id cls(const char *name) { return (id)objc_getClass(name); }
static SEL sel(const char *name) { return sel_registerName(name); }

static id send0(id recv, SEL s) {
  return ((id (*)(id, SEL))objc_msgSend)(recv, s);
}
static id send1(id recv, SEL s, id arg) {
  return ((id (*)(id, SEL, id))objc_msgSend)(recv, s, arg);
}
static void sendVoid1(id recv, SEL s, id arg) {
  ((void (*)(id, SEL, id))objc_msgSend)(recv, s, arg);
}

static id nsstring(const char *s) {
  return ((id (*)(id, SEL, const char *))objc_msgSend)(
      cls("NSString"), sel("stringWithUTF8String:"), s);
}

static id alloc(const char *className) { return send0(cls(className), sel("alloc")); }

static id newMenu(const char *title) {
  return ((id (*)(id, SEL, id))objc_msgSend)(alloc("NSMenu"), sel("initWithTitle:"),
                                              nsstring(title));
}

static id newMenuItem(const char *title, const char *actionSel, const char *key) {
  SEL action = actionSel ? sel(actionSel) : (SEL)0;
  return ((id (*)(id, SEL, id, SEL, id))objc_msgSend)(
      alloc("NSMenuItem"), sel("initWithTitle:action:keyEquivalent:"), nsstring(title),
      action, nsstring(key ? key : ""));
}

static id separatorItem(void) { return send0(cls("NSMenuItem"), sel("separatorItem")); }

static void menuAddItem(id menu, id item) { sendVoid1(menu, sel("addItem:"), item); }

static void itemSetSubmenu(id item, id submenu) {
  sendVoid1(item, sel("setSubmenu:"), submenu);
}

void fleetdeck_install_menu(void) {
  id app = send0(cls("NSApplication"), sel("sharedApplication"));

  id menubar = newMenu("");

  // The bold app-name menu. Quit is the one item every Mac app is expected
  // to have; without any menu at all it suffers the exact same fate as
  // Cmd+V -- Cmd+Q has nothing to route through either.
  id appMenuItem = newMenuItem("fleetdeck", NULL, "");
  id appMenu = newMenu("fleetdeck");
  menuAddItem(appMenu, newMenuItem("Quit fleetdeck", "terminate:", "q"));
  itemSetSubmenu(appMenuItem, appMenu);
  menuAddItem(menubar, appMenuItem);

  // Edit menu -- the actual fix. "Z" (capital) is the conventional Cocoa way
  // to spell Cmd+Shift+Z for Redo; AppKit reads the case of the key
  // equivalent character as the Shift modifier, no separate mask needed.
  id editMenuItem = newMenuItem("Edit", NULL, "");
  id editMenu = newMenu("Edit");
  menuAddItem(editMenu, newMenuItem("Undo", "undo:", "z"));
  menuAddItem(editMenu, newMenuItem("Redo", "redo:", "Z"));
  menuAddItem(editMenu, separatorItem());
  menuAddItem(editMenu, newMenuItem("Cut", "cut:", "x"));
  menuAddItem(editMenu, newMenuItem("Copy", "copy:", "c"));
  menuAddItem(editMenu, newMenuItem("Paste", "paste:", "v"));
  menuAddItem(editMenu, separatorItem());
  menuAddItem(editMenu, newMenuItem("Select All", "selectAll:", "a"));
  itemSetSubmenu(editMenuItem, editMenu);
  menuAddItem(menubar, editMenuItem);

  // View menu: Reload. Without it the page in this window lives until Quit --
  // the red button hides the window instead of closing it -- so a page left
  // open over a rebuilt panel kept running the old code with no way to swap
  // it short of quitting. reload: is WKWebView's own action and needs no
  // target, the same way cut: and paste: above do not: AppKit walks the
  // responder chain to the web view.
  id viewMenuItem = newMenuItem("View", NULL, "");
  id viewMenu = newMenu("View");
  menuAddItem(viewMenu, newMenuItem("Reload", "reload:", "r"));
  itemSetSubmenu(viewMenuItem, viewMenu);
  menuAddItem(menubar, viewMenuItem);

  sendVoid1(app, sel("setMainMenu:"), menubar);
}

// --- close-to-hide -----------------------------------------------------
//
// Closing the window must not decide the panel's or a session's fate (see
// main.go's package doc: since 2026-09-11 the window starts and keeps the
// panel, and the panel still outlives the window). Before this file, closing
// the window tore the underlying engine down, which is exactly backwards for
// a window meant to behave like a browser tab you can put away and come back
// to. windowShouldClose: returning NO, plus orderOut: to actually hide it,
// keeps the engine (and its WKWebView, and that view's live reconnect
// logic) alive with nothing visible -- the same object, not a new one, is
// what a Dock click brings back.

static id get_window(id self) {
  return (id)objc_getAssociatedObject(self, "fleetdeck_window");
}

static bool windowShouldClose(id self, SEL _cmd, id sender) {
  (void)_cmd;
  id window = get_window(self);
  if (window) {
    sendVoid1(window, sel("orderOut:"), (id)0);
  }
  (void)sender;
  return false;
}

static bool applicationShouldHandleReopen(id self, SEL _cmd, id app,
                                           bool hasVisibleWindows) {
  (void)_cmd;
  (void)app;
  if (!hasVisibleWindows) {
    id window = get_window(self);
    if (window) {
      sendVoid1(window, sel("makeKeyAndOrderFront:"), (id)0);
    }
  }
  return true;
}

static id create_delegate(const char *class_name, const char *protocol_name) {
  Class c = objc_lookUpClass(class_name);
  if (!c) {
    c = objc_allocateClassPair((Class)cls("NSObject"), class_name, 0);
    if (protocol_name) {
      class_addProtocol(c, objc_getProtocol(protocol_name));
    }
    objc_registerClassPair(c);
  }
  return send0(send0((id)c, sel("alloc")), sel("init"));
}

void fleetdeck_install_close_to_hide(void *window) {
  id nswindow = (id)window;
  id app = send0(cls("NSApplication"), sel("sharedApplication"));

  id windowDelegate = create_delegate("FleetdeckHideOnCloseDelegate", "NSWindowDelegate");
  class_addMethod(object_getClass(windowDelegate), sel("windowShouldClose:"),
                   (IMP)windowShouldClose, "c@:@");
  objc_setAssociatedObject(windowDelegate, "fleetdeck_window", nswindow,
                            OBJC_ASSOCIATION_ASSIGN);
  sendVoid1(nswindow, sel("setDelegate:"), windowDelegate);

  id appDelegate =
      create_delegate("FleetdeckReopenDelegate", "NSApplicationDelegate");
  class_addMethod(object_getClass(appDelegate), sel("applicationShouldHandleReopen:hasVisibleWindows:"),
                   (IMP)applicationShouldHandleReopen, "c@:@c");
  objc_setAssociatedObject(appDelegate, "fleetdeck_window", nswindow,
                            OBJC_ASSOCIATION_ASSIGN);
  sendVoid1(app, sel("setDelegate:"), appDelegate);
}

int fleetdeck_window_should_close_for_test(void *window) {
  id nswindow = (id)window;
  id delegate = ((id (*)(id, SEL))objc_msgSend)(nswindow, sel("delegate"));
  if (!delegate) {
    return -1;
  }
  // Goes through the real objc_msgSend dispatch, not a direct C call to
  // windowShouldClose -- this is what actually proves the method was
  // registered under the selector AppKit will look it up by, not just that
  // the function exists.
  bool (*dispatch)(id, SEL, id) = (bool (*)(id, SEL, id))objc_msgSend;
  return dispatch(delegate, sel("windowShouldClose:"), nswindow) ? 1 : 0;
}
