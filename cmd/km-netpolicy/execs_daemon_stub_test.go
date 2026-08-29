//go:build !linux || !amd64

package main

import (
	"bytes"
	"testing"
)

func TestRunExecsDaemon_UnsupportedPlatformIsClear(t *testing.T) {
	var out, errb bytes.Buffer
	o := opts{stdout: &out, stderr: &errb}
	if err := runExecsDaemon(o); err == nil {
		t.Fatal("want an error on an unsupported platform")
	}
	if !bytes.Contains(errb.Bytes(), []byte("linux/amd64")) {
		t.Errorf("want a message naming the platform requirement, got %q", errb.String())
	}
}
