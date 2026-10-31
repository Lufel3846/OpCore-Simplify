// Package datasets holds the static data of OpCore Simplify (Scripts/datasets).
// The *_gen.go files are generated from the Python modules by
// go/tools/gen_datasets.py; this file defines the types they use.
//
// OpCore Simplify is distributed under the BSD 3-Clause License
// (Copyright (c) 2024, lzhoang2601). See go/LICENSE.
package datasets

import (
	"fmt"
	"math/rand"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
)

// Repo is a kext's github_repo entry.
type Repo struct {
	Owner string
	Repo  string
}

// KextInfo mirrors kext_data.KextInfo. An empty ConflictGroupID stands for
// Python's None, a nil GithubRepo or DownloadInfo stands for the empty default.
type KextInfo struct {
	Name             string
	Description      string
	Category         string
	Required         bool
	MinDarwinVersion string
	MaxDarwinVersion string
	RequiresKexts    []string
	ConflictGroupID  string
	AllowForceLoad   bool
	GithubRepo       *Repo
	DownloadInfo     *plist.Dict
	// RandomDownloadID marks entries whose download id the Python module draws at
	// import time (9 random digits). NewCatalog draws a new one for each copy.
	RandomDownloadID bool
	// Checked is the selection state. The constructor sets it to Required.
	Checked bool
}

// NewCatalog returns a fresh copy of the kext catalog, with each entry in its
// constructor state (Checked = Required). Python shares one list between
// instances; the Go port gives each maestro its own copy.
func NewCatalog() []*KextInfo {
	out := make([]*KextInfo, len(kextCatalog))
	for i := range kextCatalog {
		k := kextCatalog[i]
		c := k
		c.RequiresKexts = append([]string(nil), k.RequiresKexts...)
		c.Checked = k.Required
		if k.GithubRepo != nil {
			r := *k.GithubRepo
			c.GithubRepo = &r
		}
		if k.DownloadInfo != nil {
			c.DownloadInfo = copyDict(k.DownloadInfo)
			if k.RandomDownloadID {
				c.DownloadInfo.Set("id", randomDownloadID())
			}
		}
		out[i] = &c
	}
	return out
}

// randomDownloadID mirrors int("".join(random.choices('0123456789', k=9))).
func randomDownloadID() int64 {
	return rand.Int63n(1000000000)
}

func copyDict(d *plist.Dict) *plist.Dict {
	out := plist.NewDict()
	for _, k := range d.Keys() {
		v, _ := d.Get(k)
		out.Set(k, v)
	}
	return out
}

// dictOf builds an ordered dictionary from alternating keys and values.
func dictOf(kv ...any) *plist.Dict {
	if len(kv)%2 != 0 {
		panic("datasets: dictOf needs key/value pairs")
	}
	d := plist.NewDict()
	for i := 0; i < len(kv); i += 2 {
		key, ok := kv[i].(string)
		if !ok {
			panic(fmt.Sprintf("datasets: dictOf key %v is not a string", kv[i]))
		}
		d.Set(key, kv[i+1])
	}
	return d
}

// CodecLayout mirrors codec_layouts.Layout.
type CodecLayout struct {
	ID      int
	Comment string
}

// SubsystemIDs is the second field of an UnsupportedUSBControllerIDs entry. In
// the Python data it is a tuple, except for a few entries where it is a single
// string. The two behave differently with "in" and len(), so both are kept.
type SubsystemIDs struct {
	List     []string
	Text     string
	IsString bool
}

// Value returns the Python object this field stands for: a string or a list of
// strings. The result is what the "in" and len() helpers of pyrt operate on.
func (s SubsystemIDs) Value() any {
	if s.IsString {
		return s.Text
	}
	out := make([]any, len(s.List))
	for i, v := range s.List {
		out[i] = v
	}
	return out
}

// USBControllerEntry is one tuple of pci_data.UnsupportedUSBControllerIDs.
type USBControllerEntry struct {
	Device    string
	Subsystem SubsystemIDs
}

// IDEntry is one key and value of a pci_data dictionary. Lookups are in
// insertion order, as Python's dict.get and "in" are.
type IDEntry struct {
	Key   string
	Value SubsystemIDs
}

// LookupID returns the value stored under key, and whether the key exists.
func LookupID(entries []IDEntry, key any) (SubsystemIDs, bool) {
	s, ok := key.(string)
	if !ok {
		return SubsystemIDs{}, false
	}
	for _, e := range entries {
		if e.Key == s {
			return e.Value, true
		}
	}
	return SubsystemIDs{}, false
}

// ACPIPatchInfo mirrors acpi_patch_data.PatchInfo without the mutable checked
// flag; the selection state lives in acpiguru.
type ACPIPatchInfo struct {
	Name         string
	Description  string
	FunctionName string
}
