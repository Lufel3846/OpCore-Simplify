package integrity

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

type oracleRequest struct {
	Op       string  `json:"op"`
	Folder   string  `json:"folder"`
	Manifest *string `json:"manifest"`
}

type oracleResult struct {
	Manifest map[string]any      `json:"manifest"`
	Valid    *bool               `json:"valid"`
	Issues   map[string][]string `json:"issues"`
	Message  string              `json:"message"`
}

func runOracle(t *testing.T, reqs []oracleRequest) []oracleResult {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found; parity test skipped")
	}
	repo, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(reqs)
	cmd := exec.Command(python, "-I", filepath.Join("testdata", "pyref_integrity.py"), repo)
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("python reference failed: %v", err)
	}
	var results []oracleResult
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatal(err)
	}
	return results
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// TestParityWithPythonIntegrityChecker runs the same folder through the Python
// original and the Go port: generate a manifest, then verify it after changes.
func TestParityWithPythonIntegrityChecker(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "tree")
	writeTree(t, folder, map[string]string{
		"a.txt":          "alpha",
		"sub/b.bin":      "beta\x00\x01",
		"sub/deep/c":     "",
		".hidden":        "dot",
		"Empty/file.efi": "efi",
	})

	// 1. Manifest generation.
	pyManifest := runOracle(t, []oracleRequest{{Op: "manifest", Folder: folder}})[0].Manifest
	goManifest, err := GenerateFolderManifest(folder, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(goManifest) != len(pyManifest) {
		t.Fatalf("manifest sizes differ: go %d, python %d", len(goManifest), len(pyManifest))
	}
	for k, v := range pyManifest {
		if goManifest[k] != v {
			t.Errorf("manifest[%q]: go %v, python %v", k, goManifest[k], v)
		}
	}

	// 2. Verify an unchanged tree, then after modify, delete and add.
	steps := []struct {
		name   string
		mutate func()
	}{
		{"unchanged", func() {}},
		{"modified", func() { writeTree(t, folder, map[string]string{"a.txt": "changed"}) }},
		{"missing", func() { os.Remove(filepath.Join(folder, "sub", "b.bin")) }},
		{"untracked", func() { writeTree(t, folder, map[string]string{"new/d.txt": "delta"}) }},
	}
	for _, step := range steps {
		step.mutate()
		py := runOracle(t, []oracleRequest{{Op: "verify", Folder: folder}})[0]
		goRes, err := VerifyFolderIntegrity(folder, "")
		if err != nil {
			t.Fatal(err)
		}
		if py.Valid == nil || *py.Valid != goRes.Valid {
			t.Errorf("%s: valid go %v, python %v", step.name, goRes.Valid, py.Valid)
			continue
		}
		if !reflect.DeepEqual(sortedCopy(goRes.Issues.Modified), sortedCopyOrEmpty(py.Issues["modified"])) ||
			!reflect.DeepEqual(sortedCopy(goRes.Issues.Missing), sortedCopyOrEmpty(py.Issues["missing"])) ||
			!reflect.DeepEqual(sortedCopy(goRes.Issues.Untracked), sortedCopyOrEmpty(py.Issues["untracked"])) {
			t.Errorf("%s: issues differ\n go: %+v\n py: %+v", step.name, goRes.Issues, py.Issues)
		}
	}

	// 3. Failure modes: missing folder, missing manifest, manifest not a dict.
	bad := filepath.Join(root, "absent")
	manifestList := filepath.Join(folder, "list.json")
	if err := os.WriteFile(manifestList, []byte(`["not", "a", "dict"]`), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []oracleRequest{
		{Op: "verify", Folder: bad},
		{Op: "verify", Folder: folder, Manifest: strPtr(filepath.Join(root, "nope.json"))},
		{Op: "verify", Folder: folder, Manifest: strPtr(manifestList)},
	}
	pyResults := runOracle(t, cases)
	for i, c := range cases {
		goRes, err := VerifyFolderIntegrity(c.Folder, deref(c.Manifest))
		if err != nil {
			t.Fatal(err)
		}
		if goRes.Message != pyResults[i].Message || goRes.Valid {
			t.Errorf("failure case %d: go message %q valid %v; python %q", i, goRes.Message, goRes.Valid, pyResults[i].Message)
		}
	}
}

func sortedCopyOrEmpty(in []string) []string {
	if in == nil {
		return []string{}
	}
	return sortedCopy(in)
}

func strPtr(s string) *string { return &s }

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
