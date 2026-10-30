package fetch

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyjson"
)

const plistBody = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>Kernel</key><dict><key>Patch</key><array><integer>3</integer><string>x</string></array></dict></dict></plist>`

const jsonBody = `{"b": 1, "a": [true, 2.5, null]}`

func gzipBytes(t *testing.T, data string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	w.Write([]byte(data))
	w.Close()
	return b.Bytes()
}

// newServer serves the fixtures used by both implementations. "flaky" fails
// once per run key, so the retry path is exercised.
func newServer(t *testing.T, binary []byte) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	flakyHits := map[string]int{}
	mux := http.NewServeMux()
	mux.HandleFunc("/json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, jsonBody)
	})
	mux.HandleFunc("/badjson", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"a": }`) })
	mux.HandleFunc("/plist", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, plistBody) })
	mux.HandleFunc("/text", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "héllo ünïcode") })
	mux.HandleFunc("/gzipheader", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Write(gzipBytes(t, jsonBody))
	})
	mux.HandleFunc("/gzipmagic", func(w http.ResponseWriter, r *http.Request) { w.Write(gzipBytes(t, jsonBody)) })
	mux.HandleFunc("/deflate", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "deflate")
		var b bytes.Buffer
		z := zlib.NewWriter(&b)
		z.Write([]byte(jsonBody))
		z.Close()
		w.Write(b.Bytes())
	})
	mux.HandleFunc("/badgzipheader", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		io.WriteString(w, jsonBody)
	})
	mux.HandleFunc("/notfound", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "nope", 404) })
	mux.HandleFunc("/unavailable", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "x", 500) })
	mux.HandleFunc("/nocontent", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.HandleFunc("/file", func(w http.ResponseWriter, r *http.Request) { w.Write(binary) })
	mux.HandleFunc("/empty", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("/flaky", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		flakyHits[r.URL.RawQuery]++
		first := flakyHits[r.URL.RawQuery] == 1
		mu.Unlock()
		if first {
			http.Error(w, "try again", 500)
			return
		}
		w.Write(binary)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

type fetchCase struct{ path, kind string }

type downloadCase struct {
	name, path, sha string
	flakyKey        string
}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// TestParityWithPythonResourceFetcher runs the same requests against the
// original ResourceFetcher and the Go port, using one local server.
func TestParityWithPythonResourceFetcher(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found; parity test skipped")
	}
	repo, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	binary := bytes.Repeat([]byte("OpenCore binary payload\n"), 60)
	srv := newServer(t, binary)
	url := func(p string) string { return srv.URL + p }

	fetches := []fetchCase{
		{"/json", "json"}, {"/badjson", "json"}, {"/plist", "plist"}, {"/text", "text"},
		{"/gzipheader", "json"}, {"/gzipmagic", "json"}, {"/deflate", "json"},
		{"/badgzipheader", "json"}, {"/notfound", "text"}, {"/unavailable", "json"},
		{"/nocontent", "text"}, {"/json", "text"},
	}
	downloads := []downloadCase{
		{"good sha", "/file", sha256Hex(binary), ""},
		{"upper sha", "/file", strings.ToUpper(sha256Hex(binary)), ""},
		{"wrong sha", "/file", strings.Repeat("0", 64), ""},
		{"no sha", "/file", "", ""},
		{"empty body", "/empty", "", ""},
		{"retry then good", "/flaky", sha256Hex(binary), "run"},
		{"not found", "/notfound", "", ""},
	}

	dirPy := t.TempDir()
	dirGo := t.TempDir()

	var reqs []map[string]any
	for _, f := range fetches {
		reqs = append(reqs, map[string]any{"op": "fetch", "url": url(f.path), "type": f.kind})
	}
	for i, d := range downloads {
		u := url(d.path)
		if d.flakyKey != "" {
			u += "?run=py-" + string(rune('a'+i))
		}
		var sha any
		if d.sha != "" {
			sha = d.sha
		}
		reqs = append(reqs, map[string]any{"op": "download", "url": u,
			"dest": filepath.Join(dirPy, "f"+string(rune('a'+i))), "sha256": sha})
	}

	py := runFetchOracle(t, python, repo, reqs)

	goFetcher := New(io.Discard)
	for i, f := range fetches {
		got := goFetcher.FetchAndParse(url(f.path), f.kind)
		want, pyHasValue := py[i].OK.(string)
		if !pyHasValue {
			if got != nil {
				t.Errorf("fetch %s (%s): go %v; python None", f.path, f.kind, got)
			}
			continue
		}
		if got == nil {
			t.Errorf("fetch %s (%s): go None; python %s", f.path, f.kind, want)
			continue
		}
		text, err := pyjson.Dump(got)
		if err != nil {
			t.Errorf("fetch %s: dump %v", f.path, err)
			continue
		}
		if text != want {
			t.Errorf("fetch %s (%s):\n go: %s\n py: %s", f.path, f.kind, text, want)
		}
	}

	for i, d := range downloads {
		u := url(d.path)
		if d.flakyKey != "" {
			u += "?run=go-" + string(rune('a'+i))
		}
		dest := filepath.Join(dirGo, "f"+string(rune('a'+i)))
		ok, err := goFetcher.DownloadAndSave(u, dest, d.sha)
		if err != nil {
			t.Fatalf("%s: %v", d.name, err)
		}
		goHash := ""
		if data, err := os.ReadFile(dest); err == nil {
			goHash = sha256Hex(data)
		}
		want := py[len(fetches)+i].OK.(map[string]any)
		pyOK := want["bool"].(bool)
		var pyHash string
		if want["file_sha256"] != nil {
			pyHash = want["file_sha256"].(string)
		}
		if ok != pyOK || goHash != pyHash {
			t.Errorf("download %s: go (%v, %q); python (%v, %q)", d.name, ok, goHash, pyOK, pyHash)
		}
	}
}

// fetchResult is the oracle answer: a string (json.dumps of a fetch result),
// nil (no result), or a map for downloads.
type fetchResult struct {
	OK any `json:"ok"`
}

func runFetchOracle(t *testing.T, python, repo string, reqs []map[string]any) []fetchResult {
	t.Helper()
	input, err := json.Marshal(reqs)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(python, "-I", filepath.Join("testdata", "pyref_fetch.py"), repo)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = append(os.Environ(), "NO_PROXY=*", "no_proxy=*")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("python reference failed: %v\n%s", err, stderr.String())
	}
	var results []fetchResult
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatalf("decoding python output: %v", err)
	}
	if len(results) != len(reqs) {
		t.Fatalf("python returned %d results for %d requests", len(results), len(reqs))
	}
	return results
}
