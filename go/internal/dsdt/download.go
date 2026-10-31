package dsdt

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/fsutil"
)

// URLs of DSDT.__init__ and get_latest_iasl.
const (
	IaslURLMacOS         = "https://raw.githubusercontent.com/acidanthera/MaciASL/master/Dist/iasl-stable"
	IaslURLMacOSLegacy   = "https://raw.githubusercontent.com/acidanthera/MaciASL/master/Dist/iasl-legacy"
	IaslURLLinux         = "https://raw.githubusercontent.com/corpnewt/linux_iasl/main/iasl.zip"
	IaslURLLinuxLegacy   = "https://raw.githubusercontent.com/corpnewt/iasl-legacy/main/iasl-legacy-linux.zip"
	IaslURLWindows       = "https://github.com/open-acpica/acpica/releases/download/20251212/iasl-win-20251212.zip"
	IaslURLWindowsLegacy = "https://raw.githubusercontent.com/corpnewt/iasl-legacy/main/iasl-legacy-windows.zip"
)

// FileDownloader is the part of fetch.Fetcher used to download iasl.
type FileDownloader interface {
	DownloadAndSave(url, destination, sha256Hash string) (bool, error)
}

// IaslURL mirrors the URL choice of check_iasl for the given sys.platform.
func IaslURL(platform string, legacy bool) (string, error) {
	switch {
	case platform == "darwin":
		if legacy {
			return IaslURLMacOSLegacy, nil
		}
		return IaslURLMacOS, nil
	case strings.HasPrefix(platform, "linux"):
		if legacy {
			return IaslURLLinuxLegacy, nil
		}
		return IaslURLLinux, nil
	case platform == "win32":
		if legacy {
			return IaslURLWindowsLegacy, nil
		}
		return IaslURLWindows, nil
	}
	return "", errors.New("Unknown OS")
}

// NewIaslDownload returns the Download step of IaslFinder: it mirrors the
// download branch of check_iasl plus _download_and_extract, copying every
// iasl*/acpidump* file it finds into scriptDir.
func NewIaslDownload(scriptDir, platform string, fetcher FileDownloader, log io.Writer) func(legacy bool) error {
	if log == nil {
		log = io.Discard
	}
	return func(legacy bool) error {
		url, err := IaslURL(platform, legacy)
		if err != nil {
			return err
		}

		temp, err := os.MkdirTemp("", "")
		if err != nil {
			return err
		}
		defer os.RemoveAll(temp)

		fmt.Fprintln(log, "")
		fmt.Fprintln(log, "Please wait for download iasl...")
		fmt.Fprintln(log, "")

		ztemp, err := os.MkdirTemp(temp, "")
		if err != nil {
			return err
		}
		zfile := path.Base(url)
		archive := filepath.Join(ztemp, zfile)
		if ok, err := fetcher.DownloadAndSave(url, archive, ""); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("Could not download %s", url)
		}

		searchDir := ztemp
		if strings.HasSuffix(strings.ToLower(zfile), ".zip") {
			fmt.Fprintln(log, " - Extracting")
			searchDir, err = os.MkdirTemp(temp, "")
			if err != nil {
				return err
			}
			if err := fsutil.ExtractZip(archive, searchDir); err != nil {
				return err
			}
		}

		// The original copies into Scripts/, which always exists next to it.
		// The Go binary may run from a folder without one, so create it.
		if err := os.MkdirAll(scriptDir, 0o755); err != nil {
			return err
		}

		entries, err := os.ReadDir(searchDir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			name := entry.Name()
			lower := strings.ToLower(name)
			if entry.IsDir() || !(strings.HasPrefix(lower, "iasl") || strings.HasPrefix(lower, "acpidump")) {
				continue
			}
			fmt.Fprintf(log, " - Found %s\n", name)
			mode := os.FileMode(0o644)
			if platform != "win32" {
				fmt.Fprintln(log, "   - Chmod +x")
				mode = 0o755
			}
			fmt.Fprintf(log, "   - Copying to %s directory\n", filepath.Base(scriptDir))
			if err := copyExecutable(filepath.Join(searchDir, name), filepath.Join(scriptDir, name), mode); err != nil {
				return err
			}
		}
		return nil
	}
}

func copyExecutable(src, dst string, mode os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, mode); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}
