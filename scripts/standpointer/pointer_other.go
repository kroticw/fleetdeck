//go:build !darwin

package main

import "errors"

func movePointer(bool) (result, error) {
	return result{}, errors.New("standpointer: the pointer is moved only on macOS")
}
