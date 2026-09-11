#ifndef FLEETDECK_CHOOSE_DARWIN_H
#define FLEETDECK_CHOOSE_DARWIN_H

// fleetdeck_choose_folder shows the system's folder chooser as a modal
// window, with message above the list and prompt on the confirming button,
// and returns the chosen folder's path — malloc'd, the caller frees it — or
// NULL when the person cancelled. It must run on the main thread: the web
// view calls a bound function there, which is where it is called from.
char *fleetdeck_choose_folder(const char *message, const char *prompt);

#endif
