package dsdt

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

type zipDownloader struct {
	files map[string]string
	urls  []string
}

func (d *zipDownloader) DownloadAndSave(url, destination, sha256Hash string) (bool, error) {
	d.urls = append(d.urls, url)
	out, err := os.Create(destination)
	if err != nil {
		return false, err
	}
	defer out.Close()
	w := zip.NewWriter(out)
	for name, body := range d.files {
		f, err := w.Create(name)
		if err != nil {
			return false, err
		}
		if _, err := f.Write([]byte(body)); err != nil {
			return false, err
		}
	}
	return true, w.Close()
}

func TestIaslDownloadInstallsIntoMissingScriptDir(t *testing.T) {
	scripts := filepath.Join(t.TempDir(), "bin", "Scripts")
	downloader := &zipDownloader{files: map[string]string{"iasl": "#!/bin/sh\n", "README": "x"}}

	finder := IaslFinder{
		ScriptDir: scripts,
		Platform:  "linux",
		Download:  NewIaslDownload(scripts, "linux", downloader, nil),
	}
	got := finder.Check(false)

	want := filepath.Join(scripts, "iasl")
	if got != want {
		t.Fatalf("Check() = %q, want %q", got, want)
	}
	if len(downloader.urls) != 1 || downloader.urls[0] != IaslURLLinux {
		t.Fatalf("downloaded %v, want [%s]", downloader.urls, IaslURLLinux)
	}
	info, err := os.Stat(want)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("iasl is not executable: %v", info.Mode())
	}
	if _, err := os.Stat(filepath.Join(scripts, "README")); !os.IsNotExist(err) {
		t.Fatalf("non-iasl file was copied")
	}

	// A second lookup finds the installed copy without downloading again.
	if finder.Check(false) != want || len(downloader.urls) != 1 {
		t.Fatalf("iasl was downloaded again")
	}
}

func TestIaslURLMatchesOriginal(t *testing.T) {
	cases := map[string]string{"darwin": IaslURLMacOS, "linux": IaslURLLinux, "win32": IaslURLWindows}
	for platform, want := range cases {
		if got, err := IaslURL(platform, false); err != nil || got != want {
			t.Errorf("IaslURL(%q) = %q, %v", platform, got, err)
		}
	}
	if _, err := IaslURL("freebsd", false); err == nil {
		t.Errorf("unknown OS must fail")
	}
}
