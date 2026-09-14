//go:build darwin

package main

import "net/url"

// originOf is the origin of rawURL as WebKit names a frame's security origin:
// scheme://host, with :port when the URL gives one.
func originOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// acceptSurfaceMessage says whether a binding call from a surface's web view
// is taken: only from the main frame of a page on the panel's own origin. A
// frame the page embeds, or a document of another origin loaded in it, calling
// the window's bindings is not the panel's page.
func acceptSurfaceMessage(panelURL, origin string, mainFrame bool) bool {
	want := originOf(panelURL)
	return mainFrame && want != "" && origin == want
}
