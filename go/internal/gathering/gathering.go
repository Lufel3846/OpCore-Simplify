// Package gathering ports Scripts/gathering_files.py: it downloads OpenCorePkg,
// the kexts and macserial, verifies them, extracts them into the OCK_Files
// cache, and keeps the download history and manifests up to date.
//
// Deviations from the original are listed in go/README.md. The main one is
// that kext post-processing (kext_maestro.process_kext) is injected through
// ProcessKext and must be provided before kexts that need it can be handled.
//
// OpCore Simplify is distributed under the BSD 3-Clause License
// (Copyright (c) 2024, lzhoang2801). See go/LICENSE.
package gathering

import (
	"errors"
	"fmt"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/term"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/fetch"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/fsutil"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/github"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/integrity"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/macos"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
)

// Default URLs, the same literals as gatheringFiles.__init__.
const (
	DefaultDortaniaBuildsURL = "https://raw.githubusercontent.com/dortania/build-repo/builds/latest.json"
	DefaultOcBinaryDataURL   = "https://github.com/acidanthera/OcBinaryData/archive/refs/heads/master.zip"
)

// ErrProcessKextNotPorted is returned when a downloaded kext must go through
// kext_maestro.process_kext, which is not ported yet.
var ErrProcessKextNotPorted = errors.New("kext post-processing (kext_maestro.process_kext) is not ported yet")

// Repo is the github_repo entry of a kext: {"owner": ..., "repo": ...}.
type Repo struct {
	Owner string
	Repo  string
}

// Kext is the part of kext_maestro.KextInfo used by gathering_files.
type Kext struct {
	Name         string
	Checked      bool
	DownloadInfo *plist.Dict // nil or empty means no direct download
	GithubRepo   *Repo
}

// Gatherer mirrors gatheringFiles. Its exported fields correspond to the
// instance attributes of the original, which is why they can be pointed at
// other URLs or folders.
type Gatherer struct {
	Out     io.Writer
	Fetcher *fetch.Fetcher

	DortaniaBuildsURL string
	OcBinaryDataURL   string

	TemporaryDir        string
	OckFilesDir         string
	DownloadHistoryFile string
	// ScriptsDir is the Scripts folder, where macserial is installed and the
	// datasets folder with background_picker.icns is found.
	ScriptsDir string

	// RequestInput mirrors Utils.request_input (wait for Enter). Nil means no-op.
	RequestInput func()
	// ProcessKext mirrors kext_maestro.process_kext. Nil makes kexts that need it
	// fail with ErrProcessKextNotPorted.
	ProcessKext func(tempProductDir, kextPath string) (bool, error)
}

// New returns a Gatherer that uses the same default folders as the original:
// OCK_Files next to Scripts, and a fresh temporary directory.
func New(scriptsDir string, out io.Writer) (*Gatherer, error) {
	tmp, err := os.MkdirTemp("", "ocs_")
	if err != nil {
		return nil, err
	}
	ock := filepath.Join(filepath.Dir(scriptsDir), "OCK_Files")
	return &Gatherer{
		Out:                 out,
		Fetcher:             fetch.New(out),
		DortaniaBuildsURL:   DefaultDortaniaBuildsURL,
		OcBinaryDataURL:     DefaultOcBinaryDataURL,
		TemporaryDir:        tmp,
		OckFilesDir:         ock,
		DownloadHistoryFile: filepath.Join(ock, "history.json"),
		ScriptsDir:          scriptsDir,
	}, nil
}

func (g *Gatherer) printf(format string, args ...any) {
	if g.Out != nil {
		fmt.Fprintf(g.Out, format+"\n", args...)
	}
}

func (g *Gatherer) requestInput() {
	if g.RequestInput != nil {
		g.RequestInput()
	}
}

// LoadDownloadHistory mirrors _load_download_history: a dict keyed by product
// name, built from the list stored in history.json.
func (g *Gatherer) LoadDownloadHistory() (*plist.Dict, error) {
	data, err := fsutil.ReadFile(g.DownloadHistoryFile)
	if err != nil {
		return nil, err
	}
	history := plist.NewDict()
	list, ok := data.([]any)
	if !ok {
		return history, nil
	}
	for _, item := range list {
		d, ok := item.(*plist.Dict)
		if !ok {
			return nil, errors.New("'item' is not a dict")
		}
		name, _ := d.Get("product_name")
		if s, ok := name.(string); ok && s != "" {
			history.Set(s, d)
		}
	}
	return history, nil
}

// SaveDownloadHistory mirrors _save_download_history. The file keeps the
// entries sorted by product name.
func (g *Gatherer) SaveDownloadHistory(history *plist.Dict, productName string, productID any, productURL any, sha256Hash any) error {
	entry := plist.NewDict()
	entry.Set("product_name", productName)
	entry.Set("id", productID)
	entry.Set("url", productURL)
	entry.Set("sha256", sha256Hash)
	history.Set(productName, entry)

	if err := fsutil.CreateFolder(filepath.Dir(g.DownloadHistoryFile), false); err != nil {
		return err
	}
	var items []*plist.Dict
	for _, k := range history.Keys() {
		v, _ := history.Get(k)
		items = append(items, v.(*plist.Dict))
	}
	sort.SliceStable(items, func(i, j int) bool {
		return sortName(items[i]) < sortName(items[j])
	})
	list := make([]any, 0, len(items))
	for _, it := range items {
		list = append(list, it)
	}
	return fsutil.WriteFile(g.DownloadHistoryFile, list)
}

func sortName(d *plist.Dict) string {
	v, ok := d.Get("product_name")
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// dictGet walks a path of keys in nested dicts, returning an error where Python
// would raise KeyError or TypeError.
func dictGet(v any, keys ...string) (any, error) {
	cur := v
	for _, k := range keys {
		d, ok := cur.(*plist.Dict)
		if !ok {
			return nil, fmt.Errorf("object of type %T has no key %q", cur, k)
		}
		next, ok := d.Get(k)
		if !ok {
			return nil, fmt.Errorf("KeyError: %q", k)
		}
		cur = next
	}
	return cur, nil
}

// productInfo is the dictionary of latest_products: a *plist.Dict.
type productInfo = *plist.Dict

// FetchLatestProductsInfo mirrors fetch_latest_products_info. It merges the
// history with what each checked kext and OpenCorePkg need, taking the newest
// versions from the Dortania index or from GitHub.
func (g *Gatherer) FetchLatestProductsInfo(kexts []*Kext, localHistory *plist.Dict) (*plist.Dict, error) {
	latest := plist.NewDict()
	for _, k := range localHistory.Keys() {
		v, _ := localHistory.Get(k)
		latest.Set(k, copyDict(v.(*plist.Dict)))
	}

	fetched := g.Fetcher.FetchAndParse(g.DortaniaBuildsURL, "json")
	dortania, isDict := fetched.(*plist.Dict)
	seenRepos := map[string]bool{}

	addProductInfo := func(products any) error {
		var list []any
		switch p := products.(type) {
		case *plist.Dict:
			list = []any{p}
		case []any:
			list = p
		case nil:
			return errors.New("'NoneType' object is not iterable")
		default:
			return fmt.Errorf("'%T' object is not iterable", products)
		}
		for _, item := range list {
			product, ok := item.(*plist.Dict)
			if !ok {
				return fmt.Errorf("'%T' object has no attribute 'get'", item)
			}
			nameValue, _ := product.Get("product_name")
			name, _ := nameValue.(string)
			if product.Len() == 0 || name == "" {
				continue
			}
			existing, found := latest.Get(name)
			if !found {
				latest.Set(name, product)
				continue
			}
			target := existing.(*plist.Dict)
			for _, k := range product.Keys() {
				v, _ := product.Get(k)
				target.Set(k, v)
			}
		}
		return nil
	}

	for _, kext := range kexts {
		if !kext.Checked {
			continue
		}
		switch {
		case kext.DownloadInfo != nil && kext.DownloadInfo.Len() > 0:
			if sha, _ := kext.DownloadInfo.Get("sha256"); !truthy(sha) {
				kext.DownloadInfo.Set("sha256", nil)
			}
			merged := plist.NewDict()
			merged.Set("product_name", kext.Name)
			for _, k := range kext.DownloadInfo.Keys() {
				v, _ := kext.DownloadInfo.Get(k)
				merged.Set(k, v)
			}
			if err := addProductInfo(merged); err != nil {
				return nil, err
			}
		case kext.Name == "AirportItlwm-laobamac":
			release, err := github.GetLatestRelease(g.Fetcher, "laobamac", "itlwm")
			if err != nil {
				return nil, err
			}
			for _, asset := range release.Assets {
				urlValue, _ := asset.Get("url")
				nameValue, _ := asset.Get("product_name")
				url, _ := urlValue.(string)
				name, _ := nameValue.(string)
				if strings.Contains(url, "-RELEASE") && (name == "AirportItlwm24" || name == "AirportItlwm25") {
					merged := copyDict(asset)
					merged.Set("product_name", kext.Name+name[len(name)-2:])
					if err := addProductInfo(merged); err != nil {
						return nil, err
					}
				}
			}
		case kext.GithubRepo != nil && !seenRepos[kext.GithubRepo.Repo]:
			name := kext.GithubRepo.Repo
			seenRepos[name] = true
			if !isDict {
				return nil, errors.New("'NoneType' object is not subscriptable")
			}
			if _, ok := dortania.Get(name); ok {
				release, err := releaseEntry(dortania, name)
				if err != nil {
					return nil, err
				}
				if err := addProductInfo(release); err != nil {
					return nil, err
				}
			} else {
				release, err := github.GetLatestRelease(g.Fetcher, kext.GithubRepo.Owner, kext.GithubRepo.Repo)
				if err != nil {
					return nil, err
				}
				assets := make([]any, 0, len(release.Assets))
				for _, a := range release.Assets {
					assets = append(assets, a)
				}
				if err := addProductInfo(assets); err != nil {
					return nil, err
				}
			}
		}
	}

	if !isDict {
		return nil, errors.New("'NoneType' object is not subscriptable")
	}
	opc, err := releaseEntry(dortania, "OpenCorePkg")
	if err != nil {
		return nil, err
	}
	if err := addProductInfo(opc); err != nil {
		return nil, err
	}
	return latest, nil
}

// releaseEntry builds the product dictionary from a Dortania index entry, reading
// versions[0] for id, url and sha256 exactly like the original.
func releaseEntry(index *plist.Dict, name string) (*plist.Dict, error) {
	entry, err := dictGet(index, name)
	if err != nil {
		return nil, err
	}
	id, err := dictGet(entry, "versions")
	if err != nil {
		return nil, err
	}
	versions, ok := id.([]any)
	if !ok || len(versions) == 0 {
		return nil, errors.New("IndexError: list index out of range")
	}
	first := versions[0]
	releaseID, err := dictGet(first, "release", "id")
	if err != nil {
		return nil, err
	}
	url, err := dictGet(first, "links", "release")
	if err != nil {
		return nil, err
	}
	sha, err := dictGet(first, "hashes", "release", "sha256")
	if err != nil {
		return nil, err
	}
	d := plist.NewDict()
	d.Set("product_name", name)
	d.Set("id", releaseID)
	d.Set("url", url)
	d.Set("sha256", sha)
	return d, nil
}

func copyDict(d *plist.Dict) *plist.Dict {
	out := plist.NewDict()
	for _, k := range d.Keys() {
		v, _ := d.Get(k)
		out.Set(k, v)
	}
	return out
}

// truthy mirrors Python truthiness for the JSON-like values used here.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case int64:
		return x != 0
	case float64:
		return x != 0
	case *plist.Dict:
		return x.Len() > 0
	case []any:
		return len(x) > 0
	}
	return true
}

// pyEqual mirrors Python == for the id values compared in gather_bootloader_kexts.
func pyEqual(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return reflect.DeepEqual(a, b)
}

// productNameFor maps a kext to the name it is stored under, as the chain of
// elif branches in gather_bootloader_kexts does.
func (g *Gatherer) productNameFor(kexts []*Kext, dataIndex map[string]int, name, macosVersion string) (string, error) {
	switch {
	case name == "AirportItlwm":
		version := macosVersion[:min(2, len(macosVersion))]
		skywalk, err := checkedByName(kexts, dataIndex, "IOSkywalkFamily")
		if err != nil {
			return "", err
		}
		legacy, err := checkedByName(kexts, dataIndex, "IO80211FamilyLegacy")
		if err != nil {
			return "", err
		}
		from24, err := atLeast(macosVersion, "24.0.0")
		if err != nil {
			return "", err
		}
		from234, err := atLeast(macosVersion, "23.4.0")
		if err != nil {
			return "", err
		}
		from230, err := atLeast(macosVersion, "23.0.0")
		if err != nil {
			return "", err
		}
		switch {
		case from24 && skywalk && legacy:
			version = "22"
		case from234:
			version = "23.4"
		case from230:
			version = "23.0"
		}
		return name + version, nil
	case name == "AirportItlwm-laobamac":
		return name + macosVersion[:min(2, len(macosVersion))], nil
	case strings.Contains(name, "VoodooPS2"):
		return "VoodooPS2", nil
	case name == "BlueToolFixup" || strings.HasPrefix(name, "Brcm"):
		return "BrcmPatchRAM", nil
	case strings.HasPrefix(name, "Ath3kBT"):
		return "Ath3kBT", nil
	case strings.HasPrefix(name, "IntelB"):
		return "IntelBluetoothFirmware", nil
	case strings.HasPrefix(name, "VoodooI2C"):
		return "VoodooI2C", nil
	case name == "UTBDefault":
		return "USBToolBox", nil
	}
	return name, nil
}

// atLeast mirrors "parse(v) <= parse(macos_version)" from the original.
func atLeast(macosVersion, v string) (bool, error) {
	current, err := macos.ParseVersion(macosVersion)
	if err != nil {
		return false, err
	}
	return current.Compare(mustVersion(v)) >= 0, nil
}

// copyFile mirrors shutil.copy for a single file.
func copyFile(source, destination string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(destination, data, 0o644)
}

// mustVersion parses a constant Darwin version used by this package.
func mustVersion(s string) macos.Version {
	v, err := macos.ParseVersion(s)
	if err != nil {
		panic("gathering: invalid static darwin version " + s)
	}
	return v
}

// checkedByName mirrors kexts[kext_maestro.kext_data.kext_index_by_name.get(name)].checked.
// A missing index is an error, as the Python None index raises TypeError.
func checkedByName(kexts []*Kext, dataIndex map[string]int, name string) (bool, error) {
	idx, ok := dataIndex[name]
	if !ok {
		return false, errors.New("list indices must be integers or slices, not NoneType")
	}
	if idx < 0 || idx >= len(kexts) {
		return false, errors.New("list index out of range")
	}
	return kexts[idx].Checked, nil
}

// GatherBootloaderKexts mirrors gather_bootloader_kexts. dataIndex is
// kext_data.kext_index_by_name, and kexts must be in the same order.
func (g *Gatherer) GatherBootloaderKexts(kexts []*Kext, dataIndex map[string]int, macosVersion string) (bool, error) {
	term.Head(g.Out, "Gathering Files", 68, true)
	g.printf("")
	g.printf("Please wait for download OpenCorePkg, kexts and macserial...")
	history, err := g.LoadDownloadHistory()
	if err != nil {
		return false, err
	}
	latest, err := g.FetchLatestProductsInfo(kexts, history)
	if err != nil {
		return false, err
	}
	if err := fsutil.CreateFolder(g.TemporaryDir, false); err != nil {
		return false, err
	}

	type product struct {
		name string
		kext *Kext
	}
	var products []product
	for _, k := range kexts {
		products = append(products, product{name: k.Name, kext: k})
	}
	products = append(products, product{name: "OpenCorePkg"})

	seenURLs := map[any]bool{}
	for _, p := range products {
		if p.kext != nil && !p.kext.Checked {
			continue
		}
		productName, err := g.productNameFor(kexts, dataIndex, p.name, macosVersion)
		if err != nil {
			return false, err
		}

		info, found := latest.Get(productName)
		if !found && p.kext != nil && p.kext.GithubRepo != nil {
			info, found = latest.Get(p.kext.GithubRepo.Repo)
		}
		if !found {
			g.printf("\n")
			g.printf("Could not find download URL for %s.", productName)
			continue
		}
		infoDict := info.(*plist.Dict)
		productID, _ := infoDict.Get("id")
		productURL, _ := infoDict.Get("url")
		sha256Hash, _ := infoDict.Get("sha256")

		if seenURLs[productURL] {
			continue
		}
		seenURLs[productURL] = true

		historyItem, hasHistory := history.Get(productName)
		assetDir := filepath.Join(g.OckFilesDir, productName)
		manifestPath := filepath.Join(assetDir, "manifest.json")

		if hasHistory {
			item := historyItem.(*plist.Dict)
			storedID, _ := item.Get("id")
			isLatestID := pyEqual(productID, storedID)
			verification, err := integrity.VerifyFolderIntegrity(assetDir, manifestPath)
			if err != nil {
				return false, err
			}
			if isLatestID && verification.Valid {
				g.printf("\nLatest version of %s already downloaded.", productName)
				continue
			}
		}

		g.printf("")
		if hasHistory {
			fmt.Fprint(g.Out, "Updating ")
		} else {
			fmt.Fprint(g.Out, "Please wait for download ")
		}
		g.printf("%s...", productName)
		urlText, hasURL := productURL.(string)
		if hasURL && urlText != "" {
			g.printf("from %s", urlText)
			g.printf("")
		} else {
			g.printf("")
			g.printf("Could not find download URL for %s.", productName)
			g.printf("")
			g.requestInput()
			_ = os.RemoveAll(g.TemporaryDir)
			return false, nil
		}

		zipPath := filepath.Join(g.TemporaryDir, productName) + ".zip"
		shaText, _ := sha256Hash.(string)
		ok, err := g.Fetcher.DownloadAndSave(urlText, zipPath, shaText)
		if err != nil {
			return false, err
		}
		if !ok {
			verification, err := integrity.VerifyFolderIntegrity(assetDir, manifestPath)
			if err != nil {
				return false, err
			}
			if hasHistory && verification.Valid {
				g.printf("Using previously downloaded version of %s.", productName)
				continue
			}
			return false, fmt.Errorf("Could not download %s at this time. Please try again later.", productName)
		}

		if err := fsutil.ExtractZip(zipPath, ""); err != nil {
			return false, err
		}
		if err := fsutil.CreateFolder(assetDir, true); err != nil {
			return false, err
		}

		if err := g.extractNestedZips(filepath.Join(g.TemporaryDir, productName)); err != nil {
			return false, err
		}

		if strings.Contains(productName, "OpenCore") {
			ocZip := filepath.Join(g.TemporaryDir, "OcBinaryData.zip")
			g.printf("")
			g.printf("Please wait for download OcBinaryData...")
			g.printf("from %s", g.OcBinaryDataURL)
			g.printf("")
			if _, err := g.Fetcher.DownloadAndSave(g.OcBinaryDataURL, ocZip, ""); err != nil {
				return false, err
			}
			if _, err := os.Stat(ocZip); err != nil {
				g.printf("")
				g.printf("Could not download OcBinaryData at this time.")
				g.printf("Please try again later.\n")
				g.requestInput()
				_ = os.RemoveAll(g.TemporaryDir)
				return false, nil
			}
			if err := fsutil.ExtractZip(ocZip, ""); err != nil {
				return false, err
			}
		}

		moved, err := g.MoveBootloaderKextsToProductDirectory(productName)
		if err != nil {
			return false, err
		}
		if moved {
			if _, err := integrity.GenerateFolderManifest(assetDir, manifestPath); err != nil {
				return false, err
			}
			if err := g.SaveDownloadHistory(history, productName, productID, productURL, sha256Hash); err != nil {
				return false, err
			}
		}
	}

	return true, os.RemoveAll(g.TemporaryDir)
}

// extractNestedZips mirrors the dirs_to_scan loop: every .zip found under the
// product folder is extracted next to it and removed.
func (g *Gatherer) extractNestedZips(start string) error {
	stack := []string{start}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		info, err := os.Stat(current)
		if err != nil || !info.IsDir() {
			continue
		}
		entries, err := os.ReadDir(current)
		if err != nil {
			return err
		}
		for _, e := range entries {
			itemPath := filepath.Join(current, e.Name())
			itemInfo, err := os.Stat(itemPath)
			if err == nil && itemInfo.IsDir() {
				stack = append(stack, itemPath)
			} else if strings.HasSuffix(strings.ToLower(e.Name()), ".zip") {
				if err := fsutil.ExtractZip(itemPath, ""); err != nil {
					return err
				}
				if err := os.Remove(itemPath); err != nil {
					return err
				}
				stack = append(stack, strings.TrimSuffix(itemPath, filepath.Ext(itemPath)))
			}
		}
	}
	return nil
}

// MoveBootloaderKextsToProductDirectory mirrors move_bootloader_kexts_to_product_directory.
func (g *Gatherer) MoveBootloaderKextsToProductDirectory(productName string) (bool, error) {
	if _, err := os.Stat(g.TemporaryDir); err != nil {
		return false, fmt.Errorf("the directory %s does not exist", g.TemporaryDir)
	}
	tempProductDir := filepath.Join(g.TemporaryDir, productName)

	if !strings.Contains(productName, "OpenCore") {
		matches, err := fsutil.FindMatchingPaths(tempProductDir, ".kext", "", "")
		if err != nil {
			return false, err
		}
		for _, m := range matches {
			source := filepath.Join(g.TemporaryDir, productName, m.Path)
			destination := filepath.Join(g.OckFilesDir, productName, filepath.Base(m.Path))
			lower := strings.ToLower(m.Path)
			if strings.Contains(lower, "research") || strings.Contains(lower, "debug") || strings.Contains(m.Path, "Contents") {
				continue
			}
			if g.ProcessKext == nil {
				return false, ErrProcessKextNotPorted
			}
			keep, err := g.ProcessKext(tempProductDir, m.Path)
			if err != nil {
				return false, err
			}
			if !keep {
				continue
			}
			if err := moveTree(source, destination); err != nil {
				return false, err
			}
		}
		return true, nil
	}

	return true, g.moveBootloader(productName, tempProductDir)
}

// moveBootloader is the OpenCore branch of move_bootloader_kexts_to_product_directory.
func (g *Gatherer) moveBootloader(productName, tempProductDir string) error {
	productDir := filepath.Join(g.OckFilesDir, productName)
	var destinationEFI string

	sourceBootloader := filepath.Join(g.TemporaryDir, productName, "X64", "EFI")
	if _, err := os.Stat(sourceBootloader); err == nil {
		destinationEFI = filepath.Join(productDir, filepath.Base(sourceBootloader))
		if err := moveTree(sourceBootloader, destinationEFI); err != nil {
			return err
		}
		sourceConfig := filepath.Join(filepath.Dir(filepath.Dir(sourceBootloader)), "Docs", "Sample.plist")
		destinationConfig := filepath.Join(destinationEFI, "OC", "config.plist")
		if err := moveTree(sourceConfig, destinationConfig); err != nil {
			return err
		}
	}

	ocBinaryDataDir := filepath.Join(g.TemporaryDir, "OcBinaryData", "OcBinaryData-master")
	if _, err := os.Stat(ocBinaryDataDir); err == nil {
		background := filepath.Join(g.ScriptsDir, "datasets", "background_picker.icns")
		efiDirs, err := fsutil.FindMatchingPaths(productDir, "", "EFI", "dir")
		if err != nil {
			return err
		}
		for _, efi := range efiDirs {
			entries, err := os.ReadDir(ocBinaryDataDir)
			if err != nil {
				return err
			}
			for _, e := range entries {
				source := filepath.Join(ocBinaryDataDir, e.Name())
				if destinationEFI == "" {
					// Python raises NameError here: destination_efi_path is set only
					// when the bootloader folder was moved above.
					return errors.New("local variable 'destination_efi_path' referenced before assignment")
				}
				destination := filepath.Join(destinationEFI, "OC", e.Name())
				if info, err := os.Stat(destination); err == nil && info.IsDir() {
					if err := copyTree(source, destination); err != nil {
						return err
					}
				}
			}

			resourcesImageDir := filepath.Join(productDir, efi.Path, "OC", "Resources", "Image")
			variants, err := fsutil.FindMatchingPaths(resourcesImageDir, "", "", "dir")
			if err != nil {
				return err
			}
			for _, variant := range variants {
				variantDir := filepath.Join(resourcesImageDir, variant.Path)
				names, err := os.ReadDir(variantDir)
				if err != nil {
					return err
				}
				hasIcns := false
				for _, n := range names {
					if strings.Contains(n.Name(), ".icns") {
						hasIcns = true
					}
				}
				if hasIcns {
					if err := copyFile(background, filepath.Join(variantDir, "Background.icns")); err != nil {
						return err
					}
				}
			}
		}
	}
	return g.installMacserial(productName, tempProductDir)
}

// installMacserial mirrors the macserial step of the OpenCore branch.
func (g *Gatherer) installMacserial(productName, tempProductDir string) error {
	matches, err := fsutil.FindMatchingPaths(tempProductDir, "", "macserial", "file")
	if err != nil {
		return err
	}
	for _, m := range matches {
		source := filepath.Join(g.TemporaryDir, productName, m.Path)
		destination := filepath.Join(g.ScriptsDir, filepath.Base(m.Path))
		if err := moveTree(source, destination); err != nil {
			return err
		}
		if runtime.GOOS != "windows" {
			info, err := os.Stat(destination)
			if err != nil {
				return err
			}
			if err := os.Chmod(destination, info.Mode()|0o111); err != nil {
				return err
			}
		}
	}
	return nil
}

// GetKernelPatches mirrors get_kernel_patches: the Kernel/Patch array of a
// remote plist, or an empty list with a message when that cannot be read.
func (g *Gatherer) GetKernelPatches(patchesName, patchesURL string) []any {
	response := g.Fetcher.FetchAndParse(patchesURL, "plist")
	if patches, err := dictGet(response, "Kernel", "Patch"); err == nil {
		if list, ok := patches.([]any); ok {
			return list
		}
	}
	g.printf("")
	g.printf("Unable to download %s at this time", patchesName)
	g.printf("from %s", patchesURL)
	g.printf("")
	g.printf("Please try again later or apply them manually.")
	g.printf("")
	g.requestInput()
	return []any{}
}

// GatherHardwareSniffer mirrors gather_hardware_sniffer. It does nothing outside
// Windows and returns "". On Windows it returns the path of the downloaded CLI.
// The Windows path relies on the GitHub release page and is not exercised by the
// test suite on this machine.
func (g *Gatherer) GatherHardwareSniffer(osName string, latest func(owner, repo string) (*github.Release, error)) (string, error) {
	if osName != "Windows" {
		return "", nil
	}
	term.Head(g.Out, "Gathering Hardware Sniffer", 68, true)
	const productName = "Hardware-Sniffer-CLI.exe"
	const repoOwner, repoName = "lzhoang2801", "Hardware-Sniffer"
	destination := filepath.Join(g.ScriptsDir, productName)

	release, err := latest(repoOwner, repoName)
	if err != nil {
		return "", err
	}
	var productID, productURL, sha256Hash any
	assetName := strings.SplitN(productName, ".", 2)[0]
	for _, asset := range release.Assets {
		if v, _ := asset.Get("product_name"); v == assetName {
			productID, _ = asset.Get("id")
			productURL, _ = asset.Get("url")
			sha256Hash, _ = asset.Get("sha256")
			break
		}
	}
	if !truthy(productID) || !truthy(productURL) || !truthy(sha256Hash) {
		g.printf("")
		g.printf("Could not find release information for %s.", productName)
		g.printf("Please try again later.")
		g.printf("")
		g.requestInput()
		return "", fmt.Errorf("Could not find release information for %s.", productName)
	}

	history, err := g.LoadDownloadHistory()
	if err != nil {
		return "", err
	}
	historyItem, hasHistory := history.Get(productName)
	if hasHistory {
		item := historyItem.(*plist.Dict)
		storedID, _ := item.Get("id")
		isLatest := pyEqual(productID, storedID)
		fileValid := false
		if _, err := os.Stat(destination); err == nil {
			local, _, err := integrity.SHA256(destination)
			if err != nil {
				return "", err
			}
			fileValid = pyEqual(sha256Hash, local)
		}
		if isLatest && fileValid {
			g.printf("")
			g.printf("Latest version of %s already downloaded.", productName)
			return destination, nil
		}
	}

	g.printf("")
	if hasHistory {
		fmt.Fprint(g.Out, "Updating ")
	} else {
		fmt.Fprint(g.Out, "Please wait for download ")
	}
	g.printf("%s...", productName)
	g.printf("")
	g.printf("from %v", productURL)
	g.printf("")

	ok, err := g.Fetcher.DownloadAndSave(productURL.(string), destination, sha256Hash.(string))
	if err != nil {
		return "", err
	}
	if !ok {
		g.printf("Go to https://github.com/%s/%s/releases/latest to download %s manually.", repoOwner, repoName, productName)
		g.printf("")
		g.requestInput()
		return "", fmt.Errorf("Failed to download %s.", productName)
	}
	if err := g.SaveDownloadHistory(history, productName, productID, productURL, sha256Hash); err != nil {
		return "", err
	}
	return destination, nil
}

// moveTree mirrors shutil.move for files and directories: rename, falling back
// to copy and remove when the rename is not possible.
func moveTree(source, destination string) error {
	if _, err := os.Stat(source); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	if err := os.Rename(source, destination); err == nil {
		return nil
	}
	if err := copyTree(source, destination); err != nil {
		return err
	}
	return os.RemoveAll(source)
}

// copyTree copies a file or a directory tree, overwriting existing files
// (copytree with dirs_exist_ok=True).
func copyTree(source, destination string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		data, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		return os.WriteFile(destination, data, info.Mode().Perm())
	}
	return filepath.Walk(source, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, p)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		if fi.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyTree(p, target)
	})
}
