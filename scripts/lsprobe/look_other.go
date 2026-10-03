//go:build !darwin

package main

import "errors"

// quickLook has no LaunchServices to look at off macOS.
func quickLook(string) (bool, error) {
	return false, errors.New("LaunchServices is macOS's")
}
