//go:build darwin

package main

// retiresLeftover says whether a window's start removes a bundle an earlier
// update left behind (supervisor.RetireLeftover): only a window started as the
// installed app. A window started by an update has its own takeover's removal,
// and a window started from the staging directory is not the installed app --
// it opens that and goes (staged.go), and canonicalBundle names none for it.
// A dev app is never the installed app, and removes nothing (devapp.go).
func retiresLeftover(handover, canonical string, dev bool) bool {
	return !dev && handover == "" && canonical != ""
}
