package main

// Go's toolchain does not support "import \"C\"" inside a _test.go file --
// a long-standing, still-unresolved limitation (golang/go#4030). The
// accepted workaround, used here, is to keep every cgo call a test needs in
// a plain .go file and have the _test.go file call ordinary Go functions.
// This file ships inside the real binary as a result (the functions below
// are simply never called from main()), which is the accepted cost of that
// workaround, not a design choice of its own.

/*
#cgo LDFLAGS: -framework Cocoa
#include <CoreGraphics/CGGeometry.h>
#include <objc/message.h>
#include <objc/objc.h>
#include <objc/runtime.h>
#include "menu_darwin.h"

static id t_cls(const char *name) { return (id)objc_getClass(name); }
static SEL t_sel(const char *name) { return sel_registerName(name); }
static id t_send0(id recv, SEL s) { return ((id (*)(id, SEL))objc_msgSend)(recv, s); }
static long t_sendLong0(id recv, SEL s) { return ((long (*)(id, SEL))objc_msgSend)(recv, s); }
static id t_sendLongArg(id recv, SEL s, long i) { return ((id (*)(id, SEL, long))objc_msgSend)(recv, s, i); }
static char t_sendBool0(id recv, SEL s) { return ((char (*)(id, SEL))objc_msgSend)(recv, s); }
static const char *t_cstr(id nsstr) {
  if (!nsstr) return "";
  return ((const char *(*)(id, SEL))objc_msgSend)(nsstr, t_sel("UTF8String"));
}
static SEL t_action(id item) {
  return ((SEL (*)(id, SEL))objc_msgSend)(item, t_sel("action"));
}

static id t_shared_app(void) {
  return t_send0(t_cls("NSApplication"), t_sel("sharedApplication"));
}

static id t_main_menu(void) {
  return t_send0(t_shared_app(), t_sel("mainMenu"));
}

static id t_app_delegate(void) {
  return t_send0(t_shared_app(), t_sel("delegate"));
}

static int t_menu_item_count(id menu) {
  if (!menu) return 0;
  return (int)t_sendLong0(menu, t_sel("numberOfItems"));
}

static id t_menu_item_at(id menu, int i) {
  return t_sendLongArg(menu, t_sel("itemAtIndex:"), i);
}

static id t_submenu(id item) { return t_send0(item, t_sel("submenu")); }

static const char *t_item_title(id item) { return t_cstr(t_send0(item, t_sel("title"))); }

static const char *t_item_key_equivalent(id item) {
  return t_cstr(t_send0(item, t_sel("keyEquivalent")));
}

static int t_item_has_action(id item, const char *actionName) {
  return t_action(item) == t_sel(actionName);
}

// t_create_hidden_window mirrors the real window's own construction
// (libs/webview/include/webview.h's set_up_window: same style mask, same
// zero-size CGRectMake, same NSBackingStoreBuffered) with the one line that
// shows it on screen (makeKeyAndOrderFront:) left out -- a real NSWindow,
// never put on the operator's screen.
static void *t_create_hidden_window(void) {
  id w = t_send0(t_cls("NSWindow"), t_sel("alloc"));
  CGRect rect = CGRectMake(0, 0, 0, 0);
  unsigned long style = 1 | 2 | 4; // Titled | Closable | Miniaturizable
  w = ((id (*)(id, SEL, CGRect, unsigned long, unsigned long, int))objc_msgSend)(
      w, t_sel("initWithContentRect:styleMask:backing:defer:"), rect, style, 2, 0);
  return (void *)w;
}

static char t_window_is_visible(void *window) {
  return t_sendBool0((id)window, t_sel("isVisible"));
}

// t_dispatch_reopen calls applicationShouldHandleReopen:hasVisibleWindows:
// through real objc_msgSend dispatch, the same way AppKit would when a Dock
// icon is clicked.
static int t_dispatch_reopen(id delegate, id app, char hasVisibleWindows) {
  char (*dispatch)(id, SEL, id, char) = (char (*)(id, SEL, id, char))objc_msgSend;
  return dispatch(delegate, t_sel("applicationShouldHandleReopen:hasVisibleWindows:"), app,
                   hasVisibleWindows);
}
*/
import "C"
import "unsafe"

func testMainMenuTopLevelCount() int {
	return int(C.t_menu_item_count(C.t_main_menu()))
}

// testFindTopLevelMenu returns the item titles directly under a top-level
// menu titled title -> their action selector's key equivalent, or nil if no
// such top-level menu exists.
func testEditMenuActionKeys() map[string]string {
	mainMenu := C.t_main_menu()
	n := int(C.t_menu_item_count(mainMenu))
	var edit C.id
	for i := 0; i < n; i++ {
		item := C.t_menu_item_at(mainMenu, C.int(i))
		if C.GoString(C.t_item_title(item)) == "Edit" {
			edit = C.t_submenu(item)
			break
		}
	}
	if edit == nil {
		return nil
	}

	actions := []string{"undo:", "redo:", "cut:", "copy:", "paste:", "selectAll:"}
	result := map[string]string{}
	m := int(C.t_menu_item_count(edit))
	for i := 0; i < m; i++ {
		item := C.t_menu_item_at(edit, C.int(i))
		for _, action := range actions {
			cAction := C.CString(action)
			has := C.t_item_has_action(item, cAction) != 0
			C.free(unsafe.Pointer(cAction))
			if has {
				result[action] = C.GoString(C.t_item_key_equivalent(item))
			}
		}
	}
	return result
}

// testAppMenuQuitKeyEquivalent returns the key equivalent of the app menu's
// item whose action is terminate:, or "" (with ok false) if no such item
// exists. Cmd+Q resolves through this exactly the way Cmd+V resolves
// through the Edit menu's paste: item -- see menu_darwin.c's package doc.
func testAppMenuQuitKeyEquivalent() (key string, ok bool) {
	mainMenu := C.t_main_menu()
	n := int(C.t_menu_item_count(mainMenu))
	var appMenu C.id
	for i := 0; i < n; i++ {
		item := C.t_menu_item_at(mainMenu, C.int(i))
		if C.GoString(C.t_item_title(item)) == "fleetdeck" {
			appMenu = C.t_submenu(item)
			break
		}
	}
	if appMenu == nil {
		return "", false
	}
	cAction := C.CString("terminate:")
	defer C.free(unsafe.Pointer(cAction))
	m := int(C.t_menu_item_count(appMenu))
	for i := 0; i < m; i++ {
		item := C.t_menu_item_at(appMenu, C.int(i))
		if C.t_item_has_action(item, cAction) != 0 {
			return C.GoString(C.t_item_key_equivalent(item)), true
		}
	}
	return "", false
}

// testMenuItemKey finds the item with the given action in the top-level menu
// with the given title, and returns its key equivalent.
func testMenuItemKey(menuTitle, action string) (key string, ok bool) {
	mainMenu := C.t_main_menu()
	n := int(C.t_menu_item_count(mainMenu))
	var menu C.id
	for i := 0; i < n; i++ {
		item := C.t_menu_item_at(mainMenu, C.int(i))
		if C.GoString(C.t_item_title(item)) == menuTitle {
			menu = C.t_submenu(item)
			break
		}
	}
	if menu == nil {
		return "", false
	}
	cAction := C.CString(action)
	defer C.free(unsafe.Pointer(cAction))
	m := int(C.t_menu_item_count(menu))
	for i := 0; i < m; i++ {
		item := C.t_menu_item_at(menu, C.int(i))
		if C.t_item_has_action(item, cAction) != 0 {
			return C.GoString(C.t_item_key_equivalent(item)), true
		}
	}
	return "", false
}

func testHasTopLevelMenuTitled(title string) bool {
	mainMenu := C.t_main_menu()
	n := int(C.t_menu_item_count(mainMenu))
	for i := 0; i < n; i++ {
		item := C.t_menu_item_at(mainMenu, C.int(i))
		if C.GoString(C.t_item_title(item)) == title {
			return true
		}
	}
	return false
}

func testNewHiddenWindow() unsafe.Pointer {
	return C.t_create_hidden_window()
}

func testWindowIsVisible(window unsafe.Pointer) bool {
	return C.t_window_is_visible(window) != 0
}

func testAppDelegateSet() bool {
	return C.t_app_delegate() != nil
}

func testDispatchReopen(hasVisibleWindows bool) bool {
	var flag C.char
	if hasVisibleWindows {
		flag = 1
	}
	return C.t_dispatch_reopen(C.t_app_delegate(), C.t_shared_app(), flag) != 0
}
