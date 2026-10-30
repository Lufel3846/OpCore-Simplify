package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantOut  string
		wantCode int
	}{
		{"latest includes beta", []string{"macos", "latest"}, "25.99.99\n", 0},
		{"latest stable", []string{"macos", "latest", "--stable"}, "25.99.99\n", 0},
		{"lowest", []string{"macos", "lowest"}, "17.0.0\n", 0},
		{"name", []string{"macos", "name", "25.0.0"}, "macOS Tahoe 26\n", 0},
		{"parse normalizes", []string{"macos", "parse", "+2_5.0.0"}, "25.0.0\n", 0},
		{"no args", nil, "", 2},
		{"unknown command", []string{"smbios"}, "", 2},
		{"name missing arg", []string{"macos", "name"}, "", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := run(c.args, &out, &errOut)
			if code != c.wantCode {
				t.Errorf("exit code = %d; want %d (stderr %q)", code, c.wantCode, errOut.String())
			}
			if out.String() != c.wantOut {
				t.Errorf("stdout = %q; want %q", out.String(), c.wantOut)
			}
		})
	}
}

func TestRunNameUnknownReportsError(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"macos", "name", "16.5.0"}, &out, &errOut); code != 1 {
		t.Fatalf("exit code = %d; want 1", code)
	}
	if !strings.Contains(errOut.String(), "no macOS release matches") {
		t.Errorf("stderr = %q; want unknown-release message", errOut.String())
	}
}
