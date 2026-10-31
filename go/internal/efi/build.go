// Package efi ports OCPE.build_opencore_efi from OpCore-Simplify.py: it copies
// the OpenCorePkg base into the result folder, installs the selected kexts,
// writes the generated config.plist, and removes the drivers, resources and
// tools that the config does not load.
//
// The ACPI stage needs acpi_guru.py, which is not ported yet. In the original,
// ensure_dsdt() decides whether the ACPI patches run; here the caller declares
// that outcome with ACPIUnavailable (the same result as ensure_dsdt() returning
// False), and the build fails otherwise.
//
// OpCore Simplify is distributed under the BSD 3-Clause License
// (Copyright (c) 2024, lzhoang2601). See go/LICENSE.
package efi

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/acpiguru"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/config"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/datasets"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/fsutil"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/kextmaestro"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyrt"
)

// ErrACPINotPorted is returned when the ACPI stage would have to run, which
// needs acpi_guru.py (not ported yet).
var ErrACPINotPorted = errors.New("ACPI patching (acpi_guru.py) is not ported yet; declare ACPIUnavailable when the DSDT is not needed")

// defaultPickerVariant mirrors the fallback used when PickerVariant is Auto or missing.
const defaultPickerVariant = "Acidanthera/GoldenGate"

// Builder holds the inputs of one build.
type Builder struct {
	// OCKDir is the cache folder that contains OpenCorePkg, as OCK_Files does.
	OCKDir string
	// ResultDir is the output folder (Results next to Scripts).
	ResultDir string
	// Kexts is the maestro whose checked kexts are installed.
	Kexts *kextmaestro.Maestro
	// Config generates the config.plist.
	Config *config.Prodigy
	// ACPI runs the ACPI stage (ensure_dsdt and the checked patches). Nil falls
	// back to ACPIUnavailable.
	ACPI *acpiguru.Guru
	// ACPIUnavailable declares that ensure_dsdt() would return False, so the ACPI
	// patch lists stay empty.
	ACPIUnavailable bool
}

// Build mirrors build_opencore_efi for the report and selections already made.
func (b *Builder) Build(report any, disabled any, macosVersion string, needsOCLP bool, smbiosModel string) error {
	if err := fsutil.CreateFolder(b.ResultDir, true); err != nil {
		return err
	}
	source := filepath.Join(b.OCKDir, "OpenCorePkg")
	if _, err := os.Stat(source); err != nil {
		return fmt.Errorf("Directory '%s' does not exist.", source)
	}
	if err := fsutil.CopyTree(source, b.ResultDir); err != nil {
		return err
	}

	configFile := filepath.Join(b.ResultDir, "EFI", "OC", "config.plist")
	raw, err := fsutil.ReadFile(configFile)
	if err != nil {
		return err
	}
	cfg, ok := raw.(*plist.Dict)
	if !ok || cfg.Len() == 0 {
		return fmt.Errorf("Error: The file %s does not exist.", configFile)
	}

	// ACPI: the three lists are reset; the patches run only when a DSDT is found.
	for _, key := range []string{"Add", "Delete", "Patch"} {
		if err := setKey(cfg, []any{}, "ACPI", key); err != nil {
			return err
		}
	}
	if b.ACPI != nil {
		if err := b.runACPI(cfg, report, disabled, smbiosModel); err != nil {
			return err
		}
	} else if !b.ACPIUnavailable {
		return ErrACPINotPorted
	}

	kextsDir := filepath.Join(b.ResultDir, "EFI", "OC", "Kexts")
	if err := b.Kexts.InstallKextsToEFI(macosVersion, kextsDir); err != nil {
		return err
	}
	kernelAdd, err := b.Kexts.LoadKexts(report, macosVersion, kextsDir)
	if err != nil {
		return err
	}
	kernelList := make([]any, 0, len(kernelAdd))
	for _, entry := range kernelAdd {
		kernelList = append(kernelList, entry)
	}
	if err := setKey(cfg, kernelList, "Kernel", "Add"); err != nil {
		return err
	}

	cfg, err = b.Config.Genarate(report, disabled, smbiosModel, macosVersion, needsOCLP, b.Kexts.Kexts, cfg)
	if err != nil {
		return err
	}
	if err := fsutil.WriteFile(configFile, cfg); err != nil {
		return err
	}

	toRemove, err := b.unusedFiles(cfg)
	if err != nil {
		return err
	}
	for _, path := range toRemove {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		// The original prints a failed removal and carries on, so errors are skipped.
		if info.IsDir() {
			_ = os.RemoveAll(path)
		} else {
			_ = os.Remove(path)
		}
	}
	return nil
}

// runACPI mirrors the ACPI block of build_opencore_efi.
func (b *Builder) runACPI(cfg *plist.Dict, report, disabled any, smbiosModel string) error {
	g := b.ACPI
	lists := map[string][]any{"Add": {}, "Delete": {}, "Patch": {}}
	ok, err := g.EnsureDSDT(false)
	if err != nil {
		return err
	}
	if ok {
		g.HardwareReport = report
		g.DisabledDevices = disabled
		g.ACPIDirectory = filepath.Join(b.ResultDir, "EFI", "OC", "ACPI")
		g.SMBIOSModel = smbiosModel
		g.LPCBusDevice, _ = g.GetLPCName(false, false, false)
		load, err := g.RunPatches(func(checked bool) {
			if i, found := datasets.KextIndexByName["ECEnabler"]; found && i < len(b.Kexts.Kexts) {
				b.Kexts.Kexts[i].Checked = checked
			}
		})
		if err != nil {
			return err
		}
		lists["Add"], lists["Delete"], lists["Patch"] = load.Add, load.Delete, load.Patch
	}
	patches, err := g.ApplyACPIPatches(lists["Patch"])
	if err != nil {
		return err
	}
	lists["Patch"] = patches
	for _, key := range []string{"Add", "Delete", "Patch"} {
		if err := setKey(cfg, lists[key], "ACPI", key); err != nil {
			return err
		}
	}
	return nil
}

// unusedFiles lists the drivers, audio resources, picker variants, tools and the
// manifest that the config does not use, in the order the original removes them.
func (b *Builder) unusedFiles(cfg *plist.Dict) ([]string, error) {
	var out []string
	oc := filepath.Join(b.ResultDir, "EFI", "OC")

	drivers := filepath.Join(oc, "Drivers")
	driverList, err := fsutil.FindMatchingPaths(drivers, ".efi", "", "")
	if err != nil {
		return nil, err
	}
	loaded, err := loadedPaths(cfg, "UEFI", "Drivers")
	if err != nil {
		return nil, err
	}
	for _, m := range driverList {
		if !loaded[m.Path] {
			out = append(out, filepath.Join(drivers, m.Path))
		}
	}

	audio := filepath.Join(oc, "Resources", "Audio")
	if _, err := os.Stat(audio); err == nil {
		out = append(out, audio)
	}

	pickerVariant, err := pickerVariantOf(cfg)
	if err != nil {
		return nil, err
	}
	imageDir := filepath.Join(oc, "Resources", "Image")
	variants, err := fsutil.FindMatchingPaths(imageDir, "", "", "dir")
	if err != nil {
		return nil, err
	}
	for _, v := range variants {
		variantPath := filepath.Join(imageDir, v.Path)
		entries, err := os.ReadDir(variantPath)
		if err != nil {
			return nil, err
		}
		hasIcns := false
		for _, e := range entries {
			if strings.Contains(e.Name(), ".icns") {
				hasIcns = true
			}
		}
		if hasIcns && !strings.Contains(v.Path, pickerVariant) {
			out = append(out, variantPath)
		}
	}

	tools := filepath.Join(oc, "Tools")
	toolList, err := fsutil.FindMatchingPaths(tools, ".efi", "", "")
	if err != nil {
		return nil, err
	}
	usedTools, err := loadedPaths(cfg, "Misc", "Tools")
	if err != nil {
		return nil, err
	}
	for _, m := range toolList {
		if !usedTools[m.Path] {
			out = append(out, filepath.Join(tools, m.Path))
		}
	}

	manifest := filepath.Join(b.ResultDir, "manifest.json")
	if _, err := os.Stat(manifest); err == nil {
		out = append(out, manifest)
	}
	return out, nil
}

// loadedPaths returns the Path values of a list of entries in the config,
// such as UEFI > Drivers or Misc > Tools.
func loadedPaths(cfg *plist.Dict, keys ...string) (map[string]bool, error) {
	var cur any = cfg
	for _, k := range keys {
		next, err := pyrt.Index(cur, k)
		if err != nil {
			return nil, err
		}
		cur = next
	}
	items, ok := cur.([]any)
	if !ok {
		return nil, fmt.Errorf("TypeError: %s is not a list", strings.Join(keys, " > "))
	}
	out := map[string]bool{}
	for _, item := range items {
		path, err := pyrt.Get(item, "Path")
		if err != nil {
			return nil, err
		}
		if s, ok := path.(string); ok {
			out[s] = true
		}
	}
	return out, nil
}

// pickerVariantOf mirrors config.get("Misc", {}).get("Boot", {}).get("PickerVariant"),
// falling back to the Acidanthera/GoldenGate variant when it is missing or Auto.
func pickerVariantOf(cfg *plist.Dict) (string, error) {
	misc, err := pyrt.GetDefault(cfg, "Misc", plist.NewDict())
	if err != nil {
		return "", err
	}
	boot, err := pyrt.GetDefault(misc, "Boot", plist.NewDict())
	if err != nil {
		return "", err
	}
	value, err := pyrt.Get(boot, "PickerVariant")
	if err != nil {
		return "", err
	}
	variant, ok := value.(string)
	if !ok || variant == "Auto" {
		variant = defaultPickerVariant
	}
	if runtime.GOOS == "windows" {
		variant = strings.ReplaceAll(variant, "/", "\\")
	}
	return variant, nil
}

// setKey mirrors root[k1]...[kn] = value for a plist dictionary.
func setKey(root *plist.Dict, value any, keys ...string) error {
	var cur any = root
	for _, k := range keys[:len(keys)-1] {
		next, err := pyrt.Index(cur, k)
		if err != nil {
			return err
		}
		cur = next
	}
	d, ok := cur.(*plist.Dict)
	if !ok {
		return fmt.Errorf("TypeError: %T does not support item assignment", cur)
	}
	d.Set(keys[len(keys)-1], value)
	return nil
}
