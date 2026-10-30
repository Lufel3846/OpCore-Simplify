package gathering

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/fetch"
)

// These two functions are not compared with the Python original in this
// suite. They are tested against their documented behavior.

func TestGatherHardwareSnifferSkipsNonWindows(t *testing.T) {
	g := &Gatherer{Out: io.Discard}
	path, err := g.GatherHardwareSniffer("Linux", nil)
	if err != nil || path != "" {
		t.Errorf("GatherHardwareSniffer(Linux) = %q, %v; want empty, nil", path, err)
	}
}

func TestGetKernelPatchesReadsKernelPatch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/patches.plist", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `<?xml version="1.0"?><plist version="1.0"><dict><key>Kernel</key><dict><key>Patch</key><array><string>one</string></array></dict></dict></plist>`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	g := &Gatherer{Out: io.Discard, Fetcher: fetch.New(io.Discard)}
	got := g.GetKernelPatches("test", srv.URL+"/patches.plist")
	if len(got) != 1 || got[0] != "one" {
		t.Errorf("GetKernelPatches = %v; want [one]", got)
	}
}

func TestGetKernelPatchesFailureReturnsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	g := &Gatherer{Out: io.Discard, Fetcher: fetch.New(io.Discard)}
	got := g.GetKernelPatches("test", srv.URL+"/missing.plist")
	if got == nil || len(got) != 0 {
		t.Errorf("GetKernelPatches on failure = %v; want empty list", got)
	}
}
