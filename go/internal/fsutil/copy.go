package fsutil

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// CopyTree mirrors shutil.copytree(src, dst, dirs_exist_ok=True) for a
// directory, and shutil.copy semantics for a single file. Existing files are
// overwritten. Symlinks are followed, as shutil does by default.
func CopyTree(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return copyFileTo(src, dst, info.Mode().Perm())
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		fi, err := os.Stat(p)
		if err != nil {
			return err
		}
		return copyFileTo(p, target, fi.Mode().Perm())
	})
}

func copyFileTo(src, dst string, perm fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
