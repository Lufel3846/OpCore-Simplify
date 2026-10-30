package gathering

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/fetch"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/integrity"
)

// fixtureZip builds an OpenCorePkg-like archive: the bootloader in X64/EFI,
// Docs/Sample.plist, and an icon in a picker variant.
func fixtureZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(f, content)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const sampleConfig = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>Misc</key><dict/></dict></plist>`

// serverState is changed between steps to reproduce updates and failures.
type serverState struct {
	mu         sync.Mutex
	id         int
	sha        string
	omitOC     bool
	omitURL    bool
	zipMissing bool
}

func TestParityWithPythonGatherBootloaderKexts(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found; parity test skipped")
	}
	repo, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	ocZip := fixtureZip(t, map[string]string{
		"Docs/Sample.plist":                                           sampleConfig,
		"X64/EFI/BOOT/BOOTX64.efi":                                    "boot",
		"X64/EFI/OC/OpenCore.efi":                                     "oc",
		"X64/EFI/OC/Drivers/HfsPlus.efi":                              "hfs",
		"X64/EFI/OC/Resources/Image/Acidanthera/GoldenGate/Left.icns": "icns",
		"IA32/ignored.txt":                                            "x",
	})
	ocBinary := fixtureZip(t, map[string]string{
		"OcBinaryData-master/Drivers/Extra.efi":  "extra",
		"OcBinaryData-master/Resources/Note.txt": "note",
	})
	ocSum := sha256.Sum256(ocZip)

	state := &serverState{id: 42, sha: hex.EncodeToString(ocSum[:])}
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/latest.json", func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.omitOC {
			io.WriteString(w, `{"Other": {}}`)
			return
		}
		url := any(srv.URL + "/OpenCore.zip")
		if state.omitURL {
			url = nil
		}
		doc := map[string]any{"OpenCorePkg": map[string]any{
			"type": "Bootloader",
			"versions": []any{map[string]any{
				"release": map[string]any{"id": state.id},
				"links":   map[string]any{"release": url},
				"hashes":  map[string]any{"release": map[string]any{"sha256": state.sha}},
			}},
		}}
		json.NewEncoder(w).Encode(doc)
	})
	mux.HandleFunc("/OpenCore.zip", func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.zipMissing {
			http.Error(w, "gone", 404)
			return
		}
		w.Write(ocZip)
	})
	mux.HandleFunc("/OcBinaryData.zip", func(w http.ResponseWriter, r *http.Request) { w.Write(ocBinary) })
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	scripts, err := filepath.Abs(filepath.Join(repo, "Scripts"))
	if err != nil {
		t.Fatal(err)
	}
	ockPy := t.TempDir()
	ockGo := t.TempDir()
	_ = ockPy

	// Each step runs against the same cache, except steps marked fresh, which
	// start from an empty OCK_Files folder so that a failure cannot fall back to
	// a previously downloaded version.
	steps := []struct {
		name   string
		fresh  bool
		mutate func()
	}{
		{"fresh install", true, func() {}},
		{"already current", false, func() {}},
		{"newer release", false, func() { state.id = 43 }},
		{"checksum mismatch with valid cache", false, func() { state.id = 44; state.sha = "0000" }},
		{"no OpenCorePkg in index", false, func() { state.omitOC = true }},
		{"download URL missing", false, func() { state.omitOC = false; state.omitURL = true }},
		{"download 404 on fresh cache", true, func() { state.omitURL = false; state.zipMissing = true }},
		{"checksum mismatch on fresh cache", true, func() { state.zipMissing = false; state.sha = "0000" }},
	}

	for _, step := range steps {
		step.mutate()
		if step.fresh {
			ockPy = t.TempDir()
			ockGo = t.TempDir()
		}
		t.Run(step.name, func(t *testing.T) {
			py := runGatherOracle(t, python, repo, map[string]any{
				"dortania_url": srv.URL + "/latest.json",
				"ocbinary_url": srv.URL + "/OcBinaryData.zip",
				"ock_dir":      ockPy,
				"macos":        "25.0.0",
			})

			g := &Gatherer{
				Out:               io.Discard,
				Fetcher:           fetch.New(io.Discard),
				DortaniaBuildsURL: srv.URL + "/latest.json",
				OcBinaryDataURL:   srv.URL + "/OcBinaryData.zip",
				OckFilesDir:       ockGo,
				ScriptsDir:        scripts,
			}
			g.DownloadHistoryFile = filepath.Join(ockGo, "history.json")
			tmp, err := os.MkdirTemp("", "ocs_go_")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(tmp)
			g.TemporaryDir = tmp

			ok, goErr := g.GatherBootloaderKexts(nil, nil, "25.0.0")
			if (goErr != nil) != (py.Error != nil) {
				t.Fatalf("error mismatch: go %v; python %v", goErr, py.Error)
			}
			if goErr == nil && py.OK != nil && ok != *py.OK {
				t.Errorf("result: go %v; python %v", ok, *py.OK)
			}

			goTree, goHistory := snapshotGo(t, ockGo)
			t.Logf("go ok=%v err=%v files=%d history=%v | python ok=%v err=%s files=%d",
				ok, goErr, len(goTree), goHistory != nil, derefBool(py.OK), derefStr(py.Error), len(py.Tree))
			if !reflect.DeepEqual(goTree, py.Tree) {
				t.Errorf("tree differs\n go: %v\n py: %v", goTree, py.Tree)
			}
			if !equalHistory(goHistory, py.History) {
				t.Errorf("history differs\n go: %s\n py: %s", derefStr(goHistory), derefStr(py.History))
			}
		})
	}
}

type gatherResult struct {
	OK      *bool          `json:"ok"`
	Error   *string        `json:"error"`
	Tree    map[string]any `json:"tree"`
	History *string        `json:"history"`
}

func runGatherOracle(t *testing.T, python, repo string, req map[string]any) struct {
	Error   *string
	OK      *bool
	Tree    map[string]any
	History *string
} {
	t.Helper()
	input, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(python, "-I", filepath.Join("testdata", "pyref_gathering.py"), repo)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = append(os.Environ(), "NO_PROXY=*", "no_proxy=*")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("python reference failed: %v", err)
	}
	var res gatherResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatal(err)
	}
	return struct {
		Error   *string
		OK      *bool
		Tree    map[string]any
		History *string
	}{res.Error, res.OK, res.Tree, res.History}
}

// snapshotGo produces the same tree shape as the Python oracle: sha256 for
// files, and the parsed dict for manifest.json.
func snapshotGo(t *testing.T, root string) (map[string]any, *string) {
	t.Helper()
	tree := map[string]any{}
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if filepath.Base(p) == "manifest.json" {
			data, _ := os.ReadFile(p)
			m := map[string]any{}
			if err := json.Unmarshal(data, &m); err != nil {
				t.Fatal(err)
			}
			tree[rel] = map[string]any{"manifest": m}
			return nil
		}
		sum, _, _ := integrity.SHA256(p)
		tree[rel] = sum
		return nil
	})
	if data, err := os.ReadFile(filepath.Join(root, "history.json")); err == nil {
		s := string(data)
		return tree, &s
	}
	return tree, nil
}

func equalHistory(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func derefStr(s *string) string {
	if s == nil {
		return "<none>"
	}
	return *s
}

func derefBool(b *bool) any {
	if b == nil {
		return nil
	}
	return *b
}
