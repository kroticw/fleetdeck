// Throwaway spike for T-056: does NSGlassEffectView placed ABOVE a WKWebView
// sample the web content under it, and does it keep sampling while that content
// moves and while the window moves (the macOS 26.2 report)?
//
// Plain C over the Objective-C runtime, the way cmd/fleetdeck-window calls AppKit:
// no .m file, no Objective-C compiler mode. Loads inline HTML only: no port, no
// panel, no app bundle. The program photographs its own window with
// /usr/sbin/screencapture and quits by itself.
//
// Build: clang -x c glass-spike.c -framework AppKit -framework WebKit -o glass-spike
// Run:   ./glass-spike <out-dir> light|dark

#include <objc/message.h>
#include <objc/runtime.h>
#include <CoreGraphics/CoreGraphics.h>
#include <dispatch/dispatch.h>
#include <spawn.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/wait.h>

extern char **environ;

static id cls(const char *name) { return (id)objc_getClass(name); }
static id msg(id o, const char *s) { return ((id (*)(id, SEL))objc_msgSend)(o, sel_registerName(s)); }
static id msg1(id o, const char *s, id a) { return ((id (*)(id, SEL, id))objc_msgSend)(o, sel_registerName(s), a); }
static id str(const char *c) {
  return ((id (*)(id, SEL, const char *))objc_msgSend)(cls("NSString"), sel_registerName("stringWithUTF8String:"), c);
}
static const char *cstr(id s) { return ((const char *(*)(id, SEL))objc_msgSend)(s, sel_registerName("UTF8String")); }
static id alloc_frame(id c, CGRect r) {
  return ((id (*)(id, SEL, CGRect))objc_msgSend)(msg(c, "alloc"), sel_registerName("initWithFrame:"), r);
}

static const char *BOARD_HTML =
  "<!doctype html><meta charset='utf-8'><style>"
  ":root{color-scheme:light dark}"
  "body{margin:0;height:100vh;overflow:hidden;font:600 22px -apple-system;background:#f5f6f8;color:#1a1d22}"
  "@media (prefers-color-scheme:dark){body{background:#14161a;color:#e7e9ec}}"
  ".track{position:absolute;top:120px;left:0;display:flex;gap:18px;animation:slide 5s linear infinite}"
  ".track2{top:420px;animation-duration:7s;animation-direction:reverse}"
  ".c{flex:none;width:220px;height:240px;border-radius:12px;padding:16px;box-sizing:border-box;color:#fff}"
  ".a{background:#1f7a72}.b{background:#c92a2a}.d{background:#a15c00}.e{background:#5a3f9a}.f{background:#2f6fd0}"
  "@keyframes slide{from{transform:translateX(0)}to{transform:translateX(-50%)}}"
  "h1{position:absolute;top:24px;left:400px;margin:0;font-size:40px}"
  "</style><h1>доска под стеклом</h1>"
  "<div class='track'>"
  "<div class='c a'>T-056 active</div><div class='c b'>T-034 blocked</div><div class='c d'>T-055 review</div><div class='c e'>T-043 new</div><div class='c f'>T-053 done</div>"
  "<div class='c a'>T-056 active</div><div class='c b'>T-034 blocked</div><div class='c d'>T-055 review</div><div class='c e'>T-043 new</div><div class='c f'>T-053 done</div>"
  "</div><div class='track track2'>"
  "<div class='c f'>T-049</div><div class='c e'>T-052</div><div class='c a'>T-051</div><div class='c d'>T-021</div><div class='c b'>T-054</div>"
  "<div class='c f'>T-049</div><div class='c e'>T-052</div><div class='c a'>T-051</div><div class='c d'>T-021</div><div class='c b'>T-054</div>"
  "</div>";

// The sessions column: a second web view, transparent, as the glass's content.
static const char *PANEL_HTML =
  "<!doctype html><meta charset='utf-8'><style>"
  ":root{color-scheme:light dark}"
  "body{margin:0;padding:52px 14px 14px;background:transparent;font:600 15px -apple-system;color:#1a1d22}"
  ".row{margin-top:10px;padding:10px 12px;border-radius:12px;background:#fff;font-weight:600}"
  ".row span{display:block;margin-top:4px;font:400 12px -apple-system;color:#5b6470}"
  ".muted{font:400 13px -apple-system;color:#434a54}"
  "@media (prefers-color-scheme:dark){body{color:#e7e9ec}.row{background:#1b1e24}.row span{color:#9aa1ac}.muted{color:#c1c7cf}}"
  "</style>Сессии<div class='muted'>прямо на стекле: WKWebView без фона</div>"
  "<div class='row'>fleetdeck: чужая сборка<span>остров --surface внутри стекла</span></div>"
  "<div class='row'>fleetdeck: liquid glass<span>working · 12s</span></div>";

static char out_dir[1024];
static long window_number;
static id the_window;

static void shoot(void *name) {
  char path[1200];
  char id_arg[32];
  snprintf(path, sizeof path, "%s/%s.png", out_dir, (const char *)name);
  snprintf(id_arg, sizeof id_arg, "-l%ld", window_number);
  char *argv[] = {"/usr/sbin/screencapture", "-x", "-o", id_arg, path, NULL};
  pid_t pid;
  int rc = posix_spawn(&pid, argv[0], NULL, NULL, argv, environ);
  int status = 0;
  if (rc == 0) waitpid(pid, &status, 0);
  printf("shot %s: spawn=%d exit=%d\n", path, rc, WIFEXITED(status) ? WEXITSTATUS(status) : -1);
  fflush(stdout);
}

static void move_window(void *unused) {
  (void)unused;
  CGRect f = ((CGRect(*)(id, SEL))objc_msgSend)(the_window, sel_registerName("frame"));
  CGPoint p = {f.origin.x + 160, f.origin.y - 60};
  ((void (*)(id, SEL, CGPoint))objc_msgSend)(the_window, sel_registerName("setFrameOrigin:"), p);
  printf("moved window by +160,-60\n");
  fflush(stdout);
}

static void quit(void *unused) {
  (void)unused;
  msg1(msg(cls("NSApplication"), "sharedApplication"), "terminate:", NULL);
}

static void after(double seconds, void *ctx, dispatch_function_t fn) {
  dispatch_after_f(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(seconds * NSEC_PER_SEC)), dispatch_get_main_queue(), ctx, fn);
}

int main(int argc, char **argv) {
  if (argc < 3) {
    fprintf(stderr, "usage: glass-spike <out-dir> light|dark\n");
    return 2;
  }
  snprintf(out_dir, sizeof out_dir, "%s", argv[1]);
  int dark = strcmp(argv[2], "dark") == 0;

  id app = msg(cls("NSApplication"), "sharedApplication");
  ((void (*)(id, SEL, long))objc_msgSend)(app, sel_registerName("setActivationPolicy:"), 0);
  msg1(app, "setAppearance:", msg1(cls("NSAppearance"), "appearanceNamed:", str(dark ? "NSAppearanceNameDarkAqua" : "NSAppearanceNameAqua")));
  msg(app, "finishLaunching");

  id info = msg(cls("NSProcessInfo"), "processInfo");
  BOOL reduce = ((BOOL(*)(id, SEL))objc_msgSend)(msg(cls("NSWorkspace"), "sharedWorkspace"), sel_registerName("accessibilityDisplayShouldReduceTransparency"));
  Class glass_class = objc_getClass("NSGlassEffectView");
  printf("os: %s\nreduce transparency: %d\nNSGlassEffectView: %s\n", cstr(msg(info, "operatingSystemVersionString")), reduce, glass_class ? "present" : "absent");
  fflush(stdout);

  CGRect frame = {{240, 200}, {1200, 760}};
  unsigned long mask = 1 | 2 | 4 | 8 | (1UL << 15); // titled, closable, miniaturizable, resizable, full-size content view
  id win = ((id (*)(id, SEL, CGRect, unsigned long, unsigned long, BOOL))objc_msgSend)(
      msg(cls("NSWindow"), "alloc"), sel_registerName("initWithContentRect:styleMask:backing:defer:"), frame, mask, 2UL, NO);
  the_window = win;
  ((void (*)(id, SEL, BOOL))objc_msgSend)(win, sel_registerName("setTitlebarAppearsTransparent:"), YES);
  ((void (*)(id, SEL, BOOL))objc_msgSend)(win, sel_registerName("setReleasedWhenClosed:"), NO);
  msg1(win, "setTitle:", str("glass-spike"));

  id content = msg(win, "contentView");
  CGRect bounds = ((CGRect(*)(id, SEL))objc_msgSend)(content, sel_registerName("bounds"));

  // The board: a web view filling the window, underneath everything.
  id config = msg(msg(cls("WKWebViewConfiguration"), "alloc"), "init");
  id board = ((id (*)(id, SEL, CGRect, id))objc_msgSend)(msg(cls("WKWebView"), "alloc"), sel_registerName("initWithFrame:configuration:"), bounds, config);
  ((void (*)(id, SEL, unsigned long))objc_msgSend)(board, sel_registerName("setAutoresizingMask:"), 18UL);
  ((id (*)(id, SEL, id, id))objc_msgSend)(board, sel_registerName("loadHTMLString:baseURL:"), str(BOARD_HTML), NULL);
  msg1(content, "addSubview:", board);

  if (glass_class) {
    // The sessions column: glass above the board, a transparent web view as its content.
    CGRect panel_rect = {{bounds.size.width - 16 - 340, 16}, {340, bounds.size.height - 32}};
    id panel = alloc_frame((id)glass_class, panel_rect);
    ((void (*)(id, SEL, double))objc_msgSend)(panel, sel_registerName("setCornerRadius:"), 18.0);
    ((void (*)(id, SEL, unsigned long))objc_msgSend)(panel, sel_registerName("setAutoresizingMask:"), 1UL | 16UL);
    CGRect inner_rect = {{0, 0}, panel_rect.size};
    id inner_config = msg(msg(cls("WKWebViewConfiguration"), "alloc"), "init");
    id inner = ((id (*)(id, SEL, CGRect, id))objc_msgSend)(msg(cls("WKWebView"), "alloc"), sel_registerName("initWithFrame:configuration:"), inner_rect, inner_config);
    ((void (*)(id, SEL, id, id))objc_msgSend)(inner, sel_registerName("setValue:forKey:"),
        ((id (*)(id, SEL, BOOL))objc_msgSend)(cls("NSNumber"), sel_registerName("numberWithBool:"), NO), str("drawsBackground"));
    ((id (*)(id, SEL, id, id))objc_msgSend)(inner, sel_registerName("loadHTMLString:baseURL:"), str(PANEL_HTML), NULL);
    msg1(panel, "setContentView:", inner);
    msg1(content, "addSubview:", panel);

    // A capsule over the board: glass with a native label, style 1 (expected: Clear).
    CGRect cap_rect = {{380, bounds.size.height - 64}, {300, 40}};
    id capsule = alloc_frame((id)glass_class, cap_rect);
    ((void (*)(id, SEL, double))objc_msgSend)(capsule, sel_registerName("setCornerRadius:"), 20.0);
    ((void (*)(id, SEL, long))objc_msgSend)(capsule, sel_registerName("setStyle:"), 1L);
    ((void (*)(id, SEL, unsigned long))objc_msgSend)(capsule, sel_registerName("setAutoresizingMask:"), 8UL);
    id label = msg1(cls("NSTextField"), "labelWithString:", str("капсула: NSGlassEffectView, style 1"));
    msg1(capsule, "setContentView:", label);
    msg1(content, "addSubview:", capsule);

    // A second capsule, style 0 (expected: Regular), for comparison.
    CGRect cap2_rect = {{700, bounds.size.height - 64}, {140, 40}};
    id capsule2 = alloc_frame((id)glass_class, cap2_rect);
    ((void (*)(id, SEL, double))objc_msgSend)(capsule2, sel_registerName("setCornerRadius:"), 20.0);
    ((void (*)(id, SEL, long))objc_msgSend)(capsule2, sel_registerName("setStyle:"), 0L);
    ((void (*)(id, SEL, unsigned long))objc_msgSend)(capsule2, sel_registerName("setAutoresizingMask:"), 8UL);
    msg1(capsule2, "setContentView:", msg1(cls("NSTextField"), "labelWithString:", str("style 0")));
    msg1(content, "addSubview:", capsule2);
  }

  ((void (*)(id, SEL, id))objc_msgSend)(win, sel_registerName("makeKeyAndOrderFront:"), NULL);
  ((void (*)(id, SEL, BOOL))objc_msgSend)(app, sel_registerName("activateIgnoringOtherApps:"), YES);
  window_number = ((long (*)(id, SEL))objc_msgSend)(win, sel_registerName("windowNumber"));
  printf("window: %ld\n", window_number);
  fflush(stdout);

  // Two shots 0.9 s apart while the cards slide, then a move, then a third shot.
  after(3.0, dark ? "dark-a" : "light-a", shoot);
  after(3.9, dark ? "dark-b" : "light-b", shoot);
  after(4.6, NULL, move_window);
  after(5.6, dark ? "dark-moved" : "light-moved", shoot);
  after(6.6, NULL, quit);

  msg(app, "run");
  return 0;
}
