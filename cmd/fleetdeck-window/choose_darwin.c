// The folder chooser for the setup page, in raw Objective-C runtime calls like
// menu_darwin.c, for the same reason: webview_go's UI delegate implements only
// the file-open panel a page's <input type=file> asks for, and a page cannot
// ask it for a folder whose path it gets to see.
#include "choose_darwin.h"

#include <objc/message.h>
#include <objc/objc.h>
#include <objc/runtime.h>
#include <stdbool.h>
#include <stdlib.h>
#include <string.h>

// NSModalResponseOK.
static const long modalResponseOK = 1;

static id cls(const char *name) { return (id)objc_getClass(name); }
static SEL sel(const char *name) { return sel_registerName(name); }

static id send0(id recv, SEL s) { return ((id (*)(id, SEL))objc_msgSend)(recv, s); }
static void sendBool(id recv, SEL s, bool v) {
  ((void (*)(id, SEL, BOOL))objc_msgSend)(recv, s, v ? YES : NO);
}
static void sendObj(id recv, SEL s, id arg) {
  ((void (*)(id, SEL, id))objc_msgSend)(recv, s, arg);
}
static id nsstring(const char *s) {
  return ((id (*)(id, SEL, const char *))objc_msgSend)(cls("NSString"), sel("stringWithUTF8String:"),
                                                        s ? s : "");
}

char *fleetdeck_choose_folder(const char *message, const char *prompt) {
  id panel = send0(cls("NSOpenPanel"), sel("openPanel"));
  if (!panel) return NULL;
  sendBool(panel, sel("setCanChooseDirectories:"), true);
  sendBool(panel, sel("setCanChooseFiles:"), false);
  sendBool(panel, sel("setCanCreateDirectories:"), true);
  sendBool(panel, sel("setAllowsMultipleSelection:"), false);
  if (message && *message) sendObj(panel, sel("setMessage:"), nsstring(message));
  if (prompt && *prompt) sendObj(panel, sel("setPrompt:"), nsstring(prompt));

  long response = ((long (*)(id, SEL))objc_msgSend)(panel, sel("runModal"));
  if (response != modalResponseOK) return NULL;

  id url = send0(panel, sel("URL"));
  if (!url) return NULL;
  id path = send0(url, sel("path"));
  if (!path) return NULL;
  const char *fs = ((const char *(*)(id, SEL))objc_msgSend)(path, sel("fileSystemRepresentation"));
  return fs ? strdup(fs) : NULL;
}
