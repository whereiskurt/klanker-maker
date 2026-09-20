//go:build !linux

package main

import "errors"

// newRealSystem exists on non-linux only so the package builds and its tests
// run on a developer machine; every verb goes through the fake there.
func newRealSystem() (System, error) {
	return nil, errors.New("km-volumes: only runs on linux")
}
