//go:build darwin

package main

import "testing"

func TestASurfaceStaysOnItsOwnPage(t *testing.T) {
	const panel = "http://127.0.0.1:7777/"
	const page = "http://127.0.0.1:7777/?fleet=work"
	cases := []struct {
		target string
		want   navDecision
	}{
		{"http://127.0.0.1:7777/?fleet=work", navDecision{Allow: true}},
		{"http://127.0.0.1:7777/?fleet=work#top", navDecision{Allow: true}},
		{"http://127.0.0.1:7777/setup.html", navDecision{Board: "http://127.0.0.1:7777/setup.html"}},
		{"http://127.0.0.1:7777/?fleet=home", navDecision{Board: "http://127.0.0.1:7777/?fleet=home"}},
		{"http://127.0.0.1:7777/", navDecision{Board: "http://127.0.0.1:7777/"}},
		{"https://github.com/kroticw/fleetdeck", navDecision{External: "https://github.com/kroticw/fleetdeck"}},
		{"http://127.0.0.1:9999/?fleet=work", navDecision{External: "http://127.0.0.1:9999/?fleet=work"}},
		{"file:///etc/hosts", navDecision{}},
		{"data:text/html,hi", navDecision{}},
		{"javascript:void(0)", navDecision{}},
	}
	for _, c := range cases {
		if got := navigationDecision(panel, page, c.target); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.target, got, c.want)
		}
	}
}
