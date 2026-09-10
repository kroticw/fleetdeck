// Package version exposes the build version, injected at link time.
package version

// value is set with -ldflags "-X .../internal/version.value=<v>".
var value string

// String returns the build version, or "dev" for an unversioned build.
func String() string {
	if value == "" {
		return "dev"
	}
	return value
}
