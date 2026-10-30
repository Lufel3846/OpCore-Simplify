package macos

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// pyRequest and pyResult follow the protocol of testdata/pyref_macos.py.
type pyRequest struct {
	Op  string `json:"op"`
	Arg any    `json:"arg"`
}

type pyResult struct {
	OK    any    `json:"ok"`
	Error string `json:"error"`
}

// parityParseInputs and parityNameInputs include valid values, edge cases of
// Python's int() and malformed input. Each one is sent to the original code.
var parityParseInputs = []string{
	"25.0.0", "17.99.99", "10.13.6", "0.0.0", "+25.0.0", " 25.0.0 ", "2_5.0.0",
	"25.0", "25.0.0.0", "", "a.b.c", "25..0", "-1.0.0", "25.0.0\n",
	// Integers beyond int64 are a known difference (see go/README.md).
}

var parityNameInputs = []string{
	"25.0.0", "25", "250", "17.99.99", "19.6.0", "20.1.0", "16.5.0", "2", "0", "-1",
	"+5", "", "x", "1.", "2.0", "  25", "_5",
}

// TestParityWithPythonReference runs the original Python implementation on the
// same inputs and checks that the Go port returns the same values, or fails
// exactly where Python raises.
func TestParityWithPythonReference(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found; parity test skipped")
	}
	repo, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, "Scripts", "datasets", "os_data.py")); err != nil {
		t.Fatalf("original OpCore Simplify sources not found at %s: %v", repo, err)
	}

	reqs := []pyRequest{
		{Op: "catalog"},
		{Op: "latest", Arg: true},
		{Op: "latest", Arg: false},
		{Op: "lowest"},
	}
	for _, in := range parityParseInputs {
		reqs = append(reqs, pyRequest{Op: "parse", Arg: in})
	}
	for _, in := range parityNameInputs {
		reqs = append(reqs, pyRequest{Op: "name", Arg: in})
	}
	results := runPython(t, python, repo, reqs)

	// Catalog: rows of [name, macos_version, darwin_version, release_status].
	var goCatalog []any
	for _, r := range Releases() {
		goCatalog = append(goCatalog, []any{r.Name, r.MacOSVersion, float64(r.DarwinVersion), r.ReleaseStatus})
	}
	if !reflect.DeepEqual(results[0].OK, any(goCatalog)) {
		t.Errorf("catalog mismatch:\n go: %v\n py: %v", goCatalog, results[0].OK)
	}

	if got, want := LatestDarwinVersion(true), results[1].OK; got != want {
		t.Errorf("latest(beta=true): go %q, py %v", got, want)
	}
	if got, want := LatestDarwinVersion(false), results[2].OK; got != want {
		t.Errorf("latest(beta=false): go %q, py %v", got, want)
	}
	if got, want := LowestDarwinVersion(), results[3].OK; got != want {
		t.Errorf("lowest: go %q, py %v", got, want)
	}

	idx := 4
	for _, in := range parityParseInputs {
		py := results[idx]
		idx++
		v, err := ParseVersion(in)
		if py.Error != "" {
			if err == nil {
				t.Errorf("ParseVersion(%q): python raised %s, go returned %v", in, py.Error, v)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseVersion(%q): python returned %v, go error %v", in, py.OK, err)
			continue
		}
		want := []any{float64(v.Major), float64(v.Minor), float64(v.Patch)}
		if !reflect.DeepEqual(py.OK, any(want)) {
			t.Errorf("ParseVersion(%q) = %v; python %v", in, v, py.OK)
		}
	}

	for _, in := range parityNameInputs {
		py := results[idx]
		idx++
		name, err := MacOSName(in)
		switch {
		case py.Error != "":
			if err == nil {
				t.Errorf("MacOSName(%q): python raised %s, go returned %q", in, py.Error, name)
			}
		case py.OK == nil:
			// Python returns None when no release matches.
			if !errors.Is(err, ErrUnknownRelease) {
				t.Errorf("MacOSName(%q): python returned None, go %q, %v", in, name, err)
			}
		default:
			if err != nil || name != py.OK {
				t.Errorf("MacOSName(%q) = %q, %v; python %v", in, name, err, py.OK)
			}
		}
	}
}

func runPython(t *testing.T, python, repo string, reqs []pyRequest) []pyResult {
	t.Helper()
	input, err := json.Marshal(reqs)
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join("testdata", "pyref_macos.py")
	cmd := exec.Command(python, "-I", script, repo)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("python reference failed: %v\n%s", err, stderr.String())
	}
	var results []pyResult
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatalf("decoding python output: %v\n%s", err, out)
	}
	if len(results) != len(reqs) {
		t.Fatalf("python returned %d results for %d requests", len(results), len(reqs))
	}
	return results
}
