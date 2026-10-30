// Package fsutil ports the file helpers of Scripts/utils.py that the download
// and EFI steps use: find_matching_paths, create_folder, extract_zip_file,
// read_file and write_file.
//
// OpCore Simplify is distributed under the BSD 3-Clause License
// (Copyright (c) 2024, lzhoang2801). See go/LICENSE.
package fsutil

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyjson"
)

// Match is one entry returned by FindMatchingPaths.
type Match struct {
	Path string // relative to the searched root, joined with the OS separator
	Type string // "dir" or "file"
}

// FindMatchingPaths mirrors Utils.find_matching_paths. It walks root (without
// following directory symlinks, as os.walk does), skips names that start with
// '.', and applies the optional extension (case-insensitive), name (substring)
// and type ("dir", "file", or "" for both) filters. Results are sorted by path.
func FindMatchingPaths(root, extensionFilter, nameFilter, typeFilter string) ([]Match, error) {
	var found []Match
	valid := func(name string) bool {
		if strings.HasPrefix(name, ".") {
			return false
		}
		if extensionFilter != "" && !strings.HasSuffix(strings.ToLower(name), strings.ToLower(extensionFilter)) {
			return false
		}
		if nameFilter != "" && !strings.Contains(name, nameFilter) {
			return false
		}
		return true
	}
	err := walk(root, "", func(relRoot string, dirs, files []string) {
		if typeFilter == "" || typeFilter == "dir" {
			for _, d := range dirs {
				if valid(d) {
					found = append(found, Match{Path: filepath.Join(relRoot, d), Type: "dir"})
				}
			}
		}
		if typeFilter == "" || typeFilter == "file" {
			for _, f := range files {
				if valid(f) {
					found = append(found, Match{Path: filepath.Join(relRoot, f), Type: "file"})
				}
			}
		}
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Path < found[j].Path })
	return found, nil
}

// walk visits rel below root in os.walk order (top-down). Symlinks to
// directories are listed as directories but not descended into.
func walk(root, rel string, visit func(relRoot string, dirs, files []string)) error {
	entries, err := os.ReadDir(filepath.Join(root, rel))
	if err != nil {
		if rel == "" && os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var dirs, files []string
	for _, e := range entries {
		isDir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			if info, err := os.Stat(filepath.Join(root, rel, e.Name())); err == nil {
				isDir = info.IsDir()
			}
		}
		if isDir {
			dirs = append(dirs, e.Name())
		} else {
			files = append(files, e.Name())
		}
	}
	visit(rel, dirs, files)
	for _, d := range dirs {
		if e, err := os.Lstat(filepath.Join(root, rel, d)); err == nil && e.IsDir() {
			if err := walk(root, filepath.Join(rel, d), visit); err != nil {
				return err
			}
		}
	}
	return nil
}

// CreateFolder mirrors Utils.create_folder: if the path exists and
// removeContent is set, it is removed and recreated; otherwise it is created.
func CreateFolder(path string, removeContent bool) error {
	if _, err := os.Stat(path); err == nil {
		if removeContent {
			if err := os.RemoveAll(path); err != nil {
				return err
			}
			return os.MkdirAll(path, 0o755)
		}
		return nil
	}
	return os.MkdirAll(path, 0o755)
}

// ExtractZip mirrors Utils.extract_zip_file. The default destination is the
// archive path without its extension. Entries that would leave the destination
// (absolute paths or "..") are skipped, as zipfile.extractall does.
func ExtractZip(zipPath, destination string) error {
	if destination == "" {
		destination = strings.TrimSuffix(zipPath, filepath.Ext(zipPath))
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		name := cleanEntryName(f.Name)
		if name == "" {
			continue
		}
		target := filepath.Join(destination, name)
		if f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/") {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := writeZipEntry(f, target); err != nil {
			return err
		}
	}
	return nil
}

// cleanEntryName mirrors zipfile's sanitization: drop drive/root markers and
// empty, "." and ".." components.
func cleanEntryName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	var parts []string
	for _, p := range strings.Split(name, "/") {
		if p == "" || p == "." || p == ".." {
			continue
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, string(filepath.Separator))
}

func writeZipEntry(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.Create(target)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// ReadFile mirrors Utils.read_file. A missing path returns (nil, nil). The
// extension selects the decoder: ".json" → pyjson.Load, ".plist" → plist.Unmarshal,
// anything else returns the raw bytes.
func ReadFile(path string) (any, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	switch filepath.Ext(path) {
	case ".plist":
		return plist.Unmarshal(data)
	case ".json":
		return pyjson.Load(data)
	}
	return data, nil
}

// WriteFile mirrors Utils.write_file: ".json" is written with indent=4 (no
// trailing newline), ".plist" as XML plist, anything else as the raw bytes of
// data ([]byte or string).
func WriteFile(path string, data any) error {
	var out []byte
	switch filepath.Ext(path) {
	case ".json":
		text, err := pyjson.Dump(data)
		if err != nil {
			return err
		}
		out = []byte(text)
	case ".plist":
		b, err := plist.Marshal(data)
		if err != nil {
			return err
		}
		out = b
	default:
		switch v := data.(type) {
		case []byte:
			out = v
		case string:
			out = []byte(v)
		default:
			return fmt.Errorf("write_file: cannot write %T as raw data", data)
		}
	}
	return os.WriteFile(path, bytes.Clone(out), 0o644)
}
