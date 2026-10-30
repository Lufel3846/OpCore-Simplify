package pyjson

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
)

// jsonCases are documents read with json.loads and written with
// json.dumps(indent=4), in the form the project uses them.
var jsonCases = []string{
	`{}`,
	`[]`,
	`""`,
	`{"b": 1, "a": [1, 2.5, true, false, null]}`,
	`{"nested": {"x": {"y": []}}, "empty": {}}`,
	`"esc \" \\ \/ \b \f \n \r \t \u00e9 \ud83d\ude00"`,
	`"ascii only ~ and \u0001 control"`,
	`"日本語 çãé"`,
	`{"big": 123456789012345678901234567890, "neg": -5, "zero": 0}`,
	`[1.0, -0.0, 0.1, 1e16, 1e15, 1e-5, 1.5E+3, 2e400, -2e400]`,
	`[NaN, Infinity, -Infinity]`,
	`{"product_name": "Lilu", "id": 12345, "url": "https://x/y.zip", "sha256": null}`,
	`[{"k": "v"}, {"k": "w", "extra": [null]}]`,
	`{"dup": 1, "other": 2, "dup": 3}`,
	`  { "spaced" : [ 1 , 2 ] }  `,
	`{"a": 1,}`,
	`[1 2]`,
	`{"unterminated": "abc`,
	`"raw control ` + "\x01" + `"`,
	`01`,
	`1.`,
	`tru`,
	`{"a" 1}`,
	`[] extra`,
}

// TestParityWithPythonJSON checks Load and Dump against Python's json module.
// Python's output is the reference: the Go Dump of the Go Load must match it
// byte for byte, and invalid documents must fail in both.
func TestParityWithPythonJSON(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found; parity test skipped")
	}
	cmd := exec.Command(python, "-I", filepath.Join("testdata", "pyref_json.py"))
	input, _ := json.Marshal(jsonCases)
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("python reference failed: %v", err)
	}
	var results []struct {
		OK    *string `json:"ok"`
		Error string  `json:"error"`
	}
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatal(err)
	}
	for i, src := range jsonCases {
		py := results[i]
		v, lerr := Load([]byte(src))
		if py.Error != "" {
			if lerr == nil {
				t.Errorf("%q: python raised %s, go loaded %v", src, py.Error, v)
			}
			continue
		}
		if lerr != nil {
			t.Errorf("%q: go load error %v; python ok", src, lerr)
			continue
		}
		got, derr := Dump(v)
		if derr != nil {
			t.Errorf("%q: go dump error %v", src, derr)
			continue
		}
		if got != *py.OK {
			t.Errorf("%q:\n go: %s\n py: %s", src, got, *py.OK)
		}
	}
}
