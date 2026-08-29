package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestCapture_RejectsUnboundedDuration(t *testing.T) {
	var out, errb bytes.Buffer
	o := opts{stdout: &out, stderr: &errb}

	if code := runCapture([]string{"start", "--duration", "0"}, o); code == 0 {
		t.Fatal("a zero duration is an unbounded capture and must be refused")
	}
}

func TestCapture_RejectsDurationOverCeiling(t *testing.T) {
	var out, errb bytes.Buffer
	o := opts{stdout: &out, stderr: &errb}

	if code := runCapture([]string{"start", "--duration", "24h"}, o); code == 0 {
		t.Fatal("duration beyond the hard ceiling must be refused")
	}
	if !strings.Contains(errb.String(), maxCaptureDuration.String()) {
		t.Errorf("refusal must name the ceiling:\n%s", errb.String())
	}
}

func TestCapture_RejectsSizeOverCeiling(t *testing.T) {
	var out, errb bytes.Buffer
	o := opts{stdout: &out, stderr: &errb}

	if code := runCapture([]string{"start", "--max-size", "100GB"}, o); code == 0 {
		t.Fatal("max-size beyond the ceiling must be refused")
	}
}

func TestCapture_UnknownSubverb(t *testing.T) {
	var out, errb bytes.Buffer
	o := opts{stdout: &out, stderr: &errb}

	if code := runCapture([]string{"frobnicate"}, o); code != 2 {
		t.Fatalf("unknown subverb should exit 2, got %d", code)
	}
}

func TestParseSize(t *testing.T) {
	cases := map[string]int64{
		"250MB": 250 << 20,
		"1GB":   1 << 30,
		"512KB": 512 << 10,
		"1024":  1024,
	}
	for in, want := range cases {
		got, err := parseSize(in)
		if err != nil {
			t.Errorf("parseSize(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseSize(%q) = %d, want %d", in, got, want)
		}
	}
	if _, err := parseSize("banana"); err == nil {
		t.Error("want an error for a non-size")
	}
}
