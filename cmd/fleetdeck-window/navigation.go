//go:build darwin

package main

import "net/url"

// navDecision is what a surface's web view does with a navigation (spec 6.7):
// load it, hand it to the board, or open it in the system browser. The zero
// value cancels it.
type navDecision struct {
	Allow    bool
	Board    string
	External string
}

// navigationDecision keeps a surface on its own page. panelURL is the panel's
// address, pageURL the surface's page on it, target where the page is going.
// Another page of the panel -- the setup page, another fleet, the start page --
// opens in the board, where pages are shown; another site opens in the browser;
// anything that is not http or https goes nowhere.
func navigationDecision(panelURL, pageURL, target string) navDecision {
	t, err := url.Parse(target)
	if err != nil || (t.Scheme != "http" && t.Scheme != "https") {
		return navDecision{}
	}
	panel, err := url.Parse(panelURL)
	if err != nil {
		return navDecision{}
	}
	if t.Scheme != panel.Scheme || t.Host != panel.Host {
		return navDecision{External: target}
	}
	own, err := url.Parse(pageURL)
	if err == nil && t.Path == own.Path && t.RawQuery == own.RawQuery {
		return navDecision{Allow: true}
	}
	return navDecision{Board: target}
}
