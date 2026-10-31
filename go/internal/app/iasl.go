package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/fetch"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/fsutil"
)

// iaslURL mirrors the URL choice of DSDT.check_iasl for the current platform.
func iaslURL(platform string, legacy bool) (string, error) {
	switch {
	case platform == "darwin":
		if legacy {
			return "https://raw.githubusercontent.com/acidanthera/MaciASL/master/Dist/iasl-legacy", nil
		}
		return "https://raw.githubusercontent.com/acidanthera/MaciASL/master/Dist/iasl-stable", nil
	case strings.HasPrefix(platform, "linux"):
		if legacy {
			return "https://raw.githubusercontent.com/corpnewt/iasl-legacy/main/iasl-legacy-linux.zip", nil
		}
		return "https://raw.githubusercontent.com/corpnewt/linux_iasl/main/iasl.zip", nil
	case platform == "win32":
		if legacy {
			return "https://raw.githubusercontent.com/corpnewt/iasl-legacy/main/iasl-legacy-windows.zip", nil
		}
		// DSDT.get_latest_iasl
		return "https://github.com/open-acpica/acpica/releases/download/20251212/iasl-win-20251212.zip", nil
	}
	return "", fmt.Errorf("Unknown OS")
}

// downloadIasl mirrors DSDT._download_and_extract plus the URL selection of
// check_iasl: it downloads iasl for this platform into the Scripts folder.
func (a *App) downloadIasl(scriptDir string) func(legacy bool) error {
	return func(legacy bool) error {
		platform := pyPlatform()
		url, err := iaslURL(platform, legacy)
		if err != nil {
			return err
		}
		temp, err := os.MkdirTemp("", "")
		if err != nil {
			return err
		}
		defer os.RemoveAll(temp)

		a.U.Head("Gathering Files")
		a.println("")
		a.println("Please wait for download iasl...")
		a.println("")
		ztemp, err := os.MkdirTemp(temp, "")
		if err != nil {
			return err
		}
		zfile := url[strings.LastIndex(url, "/")+1:]
		f := fetch.New(a.U.Out)
		// The original ignores the result of download_and_save_file; a failed
		// download surfaces when the archive is opened below.
		_, _ = f.DownloadAndSave(url, filepath.Join(ztemp, zfile), "")
		searchDir := ztemp
		if strings.HasSuffix(strings.ToLower(zfile), ".zip") {
			a.println(" - Extracting")
			searchDir, err = os.MkdirTemp(temp, "")
			if err != nil {
				return err
			}
			if err := fsutil.ExtractZip(filepath.Join(ztemp, zfile), searchDir); err != nil {
				return err
			}
		}
		entries, err := os.ReadDir(searchDir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			name := e.Name()
			lower := strings.ToLower(name)
			if !strings.HasPrefix(lower, "iasl") && !strings.HasPrefix(lower, "acpidump") {
				continue
			}
			a.println(" - Found " + name)
			src := filepath.Join(searchDir, name)
			if platform != "win32" {
				a.println("   - Chmod +x")
				if err := os.Chmod(src, 0o755); err != nil {
					return err
				}
			}
			a.println("   - Copying to " + filepath.Base(scriptDir) + " directory")
			if err := os.MkdirAll(scriptDir, 0o755); err != nil {
				return err
			}
			data, err := os.ReadFile(src)
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(scriptDir, name), data, 0o755); err != nil {
				return err
			}
		}
		return nil
	}
}
