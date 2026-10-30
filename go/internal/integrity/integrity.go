// Package integrity ports Scripts/integrity_checker.py: SHA-256 of files, folder
// manifests, and verification of a folder against its manifest.
//
// OpCore Simplify is distributed under the BSD 3-Clause License
// (Copyright (c) 2024, lzhoang2601). See go/LICENSE.
package integrity

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/fsutil"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
)

// Issues is the result of VerifyFolderIntegrity when the folder and manifest
// can be read. Each list holds relative paths with '/' separators.
type Issues struct {
	Modified  []string
	Missing   []string
	Untracked []string
}

// Valid reports whether the folder matches its manifest with no issues.
func (i Issues) Valid() bool {
	return len(i.Modified) == 0 && len(i.Missing) == 0 && len(i.Untracked) == 0
}

// SHA256 mirrors IntegrityChecker.get_sha256. It returns ("", false) when the
// path does not exist or is a directory (Python returns None).
func SHA256(path string) (string, bool, error) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", false, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", false, err
	}
	return hex.EncodeToString(h.Sum(nil)), true, nil
}

// relativeFiles lists the files below folder, skipping the manifest itself, with
// their paths relative to folder. The walk follows os.walk's rules (directories
// are not followed through symlinks).
func relativeFiles(folder, manifestBase string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(folder, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// os.walk lists a symlink to a directory among the directories, not the files.
		if d.Type()&fs.ModeSymlink != 0 {
			if info, err := os.Stat(p); err == nil && info.IsDir() {
				return nil
			}
		}
		rel, err := filepath.Rel(folder, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == manifestBase {
			return nil
		}
		out = append(out, rel)
		return nil
	})
	return out, err
}

// GenerateFolderManifest mirrors IntegrityChecker.generate_folder_manifest. The
// manifest maps each relative file path to its SHA-256 and is written to
// manifestPath (default folder/manifest.json). It returns (nil, nil) when folder
// is not a directory.
func GenerateFolderManifest(folder, manifestPath string) (map[string]string, error) {
	if info, err := os.Stat(folder); err != nil || !info.IsDir() {
		return nil, nil
	}
	if manifestPath == "" {
		manifestPath = filepath.Join(folder, "manifest.json")
	}
	files, err := relativeFiles(folder, filepath.Base(manifestPath))
	if err != nil {
		return nil, err
	}
	manifest := make(map[string]string, len(files))
	for _, rel := range files {
		hash, _, err := SHA256(filepath.Join(folder, filepath.FromSlash(rel)))
		if err != nil {
			return nil, err
		}
		manifest[rel] = hash
	}
	if err := writeManifest(manifestPath, manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

// writeManifest keeps the sorted key order so the output does not depend on the
// filesystem walk order. Python writes in walk order; the set of entries is
// identical, which is what verification uses.
func writeManifest(path string, manifest map[string]string) error {
	keys := make([]string, 0, len(manifest))
	for k := range manifest {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	d := plist.NewDict()
	for _, k := range keys {
		d.Set(k, manifest[k])
	}
	return fsutil.WriteFile(path, d)
}

// Verification is the outcome of VerifyFolderIntegrity. Python returns
// (None, message) when the folder or manifest cannot be used; that case sets
// Message and leaves Issues nil, and Valid is false.
type Verification struct {
	Valid   bool
	Message string
	Issues  *Issues
}

// VerifyFolderIntegrity mirrors IntegrityChecker.verify_folder_integrity.
func VerifyFolderIntegrity(folder, manifestPath string) (Verification, error) {
	if info, err := os.Stat(folder); err != nil || !info.IsDir() {
		return Verification{Message: "Folder not found."}, nil
	}
	if manifestPath == "" {
		manifestPath = filepath.Join(folder, "manifest.json")
	}
	if _, err := os.Stat(manifestPath); err != nil {
		return Verification{Message: "Manifest file not found."}, nil
	}
	data, err := fsutil.ReadFile(manifestPath)
	if err != nil {
		return Verification{}, err
	}
	manifest, ok := data.(*plist.Dict)
	if !ok {
		return Verification{Message: "Invalid manifest file."}, nil
	}

	files, err := relativeFiles(folder, filepath.Base(manifestPath))
	if err != nil {
		return Verification{}, err
	}
	issues := &Issues{}
	actual := make(map[string]bool, len(files))
	for _, rel := range files {
		actual[rel] = true
		expected, found := manifest.Get(rel)
		if !found {
			issues.Untracked = append(issues.Untracked, rel)
			continue
		}
		current, _, err := SHA256(filepath.Join(folder, filepath.FromSlash(rel)))
		if err != nil {
			return Verification{}, err
		}
		if !sameValue(current, expected) {
			issues.Modified = append(issues.Modified, rel)
		}
	}
	for _, k := range manifest.Keys() {
		if !actual[k] {
			issues.Missing = append(issues.Missing, k)
		}
	}
	return Verification{Valid: issues.Valid(), Issues: issues}, nil
}

// sameValue compares a computed hash with a manifest value. Python compares
// the hash (a str, or None) with dict.get, so a non-string manifest value never
// matches.
func sameValue(current string, expected any) bool {
	s, ok := expected.(string)
	return ok && s == current
}
