//go:build darwin

package main

import (
	"slices"
	"strconv"
	"strings"
)

// A side surface's web view is named for its kind and its generation,
// "sessions@3". The name is set on the web view's navigation delegate and its
// message handler (surface_darwin.c) and comes back with every word from it: a
// navigation event, a navigation to decide, a page's call. After a fleet switch
// the surfaces taken down are still heard from until the effects that take
// them down are carried out, and a word routed by kind alone was taken as the
// new surface's: a stale failure asked for the page shown now again, and enough
// of them took the frame down. The generation tells the two apart.

// surfaceName is the name of kind's web view of generation gen.
func surfaceName(kind string, gen int) string {
	return kind + "@" + strconv.Itoa(gen)
}

// parseSurfaceName is the kind and generation name carries, ok false for a
// name that is no side surface's.
func parseSurfaceName(name string) (kind string, gen int, ok bool) {
	kind, digits, found := strings.Cut(name, "@")
	if !found || !slices.Contains(sideSurfaces, kind) {
		return "", 0, false
	}
	gen, err := strconv.Atoi(digits)
	if err != nil || gen < 0 || strconv.Itoa(gen) != digits {
		return "", 0, false
	}
	return kind, gen, true
}

// surfaceKind is the kind a side surface's name carries, and any other name as
// it is: what the window's log says of a surface.
func surfaceKind(name string) string {
	if kind, _, ok := parseSurfaceName(name); ok {
		return kind
	}
	return name
}

// surfaceNamed is the surface shown now under name: of its kind and its
// generation, nil for a surface taken down or a name that is no surface's.
func surfaceNamed(surfaces map[string]*surface, name string) *surface {
	kind, gen, ok := parseSurfaceName(name)
	if !ok {
		return nil
	}
	if s := surfaces[kind]; s != nil && s.gen == gen {
		return s
	}
	return nil
}
