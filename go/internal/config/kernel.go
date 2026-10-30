package config

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/datasets"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/kextmaestro"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/macos"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyrt"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/smbios"
)

// cpuIDs are the CPUID values used to spoof older or newer CPUs.
var cpuIDs = map[string]string{
	"Ivy Bridge":  "A9060300",
	"Haswell":     "C3060300",
	"Broadwell":   "D4060300",
	"Coffee Lake": "EB060800",
	"Comet Lake":  "55060A00",
	"Ice Lake":    "E5060700",
}

// mmioWhitelist mirrors Prodigy.mmio_whitelist.
func mmioWhitelist(chipset any) ([]any, error) {
	out := []any{}
	in, err := pyrt.In("Ice Lake", chipset)
	if err != nil {
		return nil, err
	}
	if in {
		out = append(out, props("Address", int64(4284481536), "Comment", "MMIO 0xFF600000", "Enabled", true))
		return out, nil
	}
	b650, err := pyrt.In("B650", chipset)
	if err != nil {
		return nil, err
	}
	if !b650 {
		x670, err := pyrt.In("X670", chipset)
		if err != nil {
			return nil, err
		}
		b650 = x670
	}
	if b650 {
		out = append(out, props("Address", int64(4244635648), "Comment", "MMIO 0xFD000000", "Enabled", true))
	}
	return out, nil
}

// addBooterPatch mirrors Prodigy.add_booter_patch. It returns the patch list
// for a Mac model whose supported range does not include the macOS version.
func addBooterPatch(smbiosModel, macosVersion string) ([]any, error) {
	device, ok := smbios.DeviceByName(smbiosModel)
	if !ok {
		return nil, fmt.Errorf("AttributeError: no device named %q", smbiosModel)
	}
	// not (initial <= macos <= last): the upper bound is parsed only when the lower holds.
	inRange, err := darwinBetween(device.InitialSupport, macosVersion, device.LastSupportedVersion)
	if err != nil {
		return nil, err
	}
	if inRange {
		return []any{}, nil
	}
	return []any{props(
		"Arch", "x86_64",
		"Comment", "Skip Board ID check",
		"Count", int64(0),
		"Enabled", true,
		"Find", hexToBytes("0050006C006100740066006F0072006D0053007500700070006F00720074002E0070006C006900730074"),
		"Identifier", "Apple",
		"Limit", int64(0),
		"Mask", hexToBytes(""),
		"Replace", hexToBytes("002E002E002E002E002E002E002E002E002E002E002E002E002E002E002E002E002E002E002E002E002E"),
		"ReplaceMask", hexToBytes(""),
		"Skip", int64(0),
	)}, nil
}

// isLowEndIntelCPU mirrors Prodigy.is_low_end_intel_cpu.
func isLowEndIntelCPU(processorName any) (bool, error) {
	for _, branding := range []string{"Celeron", "Pentium"} {
		in, err := pyrt.In(branding, processorName)
		if err != nil {
			return false, err
		}
		if in {
			return true, nil
		}
	}
	return false, nil
}

// isLowEndHaswellPlus mirrors Prodigy.is_low_end_haswell_plus.
func isLowEndHaswellPlus(processorName, codename any) (bool, error) {
	low, err := isLowEndIntelCPU(processorName)
	if err != nil || !low {
		return false, err
	}
	return pyrt.InStrings(codename, datasets.IntelCPUGenerations[:min(39, len(datasets.IntelCPUGenerations))]), nil
}

// spoofCPUID mirrors Prodigy.spoof_cpuid. ok is false where the original returns None.
func spoofCPUID(processorName, codename any, macosVersion string) (string, bool, error) {
	lowHaswell, err := isLowEndHaswellPlus(processorName, codename)
	if err != nil {
		return "", false, err
	}
	if lowHaswell {
		return cpuIDs["Ivy Bridge"], true, nil
	}
	haswell, err := pyrt.In("Haswell", codename)
	if err != nil {
		return "", false, err
	}
	if haswell {
		hedt, err := kextmaestro.IsIntelHEDTCPU(processorName, codename)
		if err != nil {
			return "", false, err
		}
		if hedt {
			return cpuIDs["Haswell"], true, nil
		}
	}
	broadwell, err := pyrt.In("Broadwell", codename)
	if err != nil {
		return "", false, err
	}
	if broadwell {
		hedt, err := kextmaestro.IsIntelHEDTCPU(processorName, codename)
		if err != nil {
			return "", false, err
		}
		if hedt {
			return cpuIDs["Broadwell"], true, nil
		}
	}
	tiger, err := pyrt.In("Tiger Lake", codename)
	if err != nil {
		return "", false, err
	}
	if tiger {
		return cpuIDs["Ice Lake"], true, nil
	}
	ice, err := pyrt.In("Ice Lake", codename)
	if err != nil {
		return "", false, err
	}
	if !ice && pyrt.InStrings(codename, datasets.IntelCPUGenerations[:min(34, len(datasets.IntelCPUGenerations))]) {
		comet, err := pyrt.In("Comet Lake", codename)
		if err != nil {
			return "", false, err
		}
		if !comet {
			return cpuIDs["Comet Lake"], true, nil
		}
		below, err := darwinBelow(macosVersion, "19.0.0")
		if err != nil {
			return "", false, err
		}
		if below {
			return cpuIDs["Coffee Lake"], true, nil
		}
	}
	return "", false, nil
}

// blockKextBundle mirrors Prodigy.block_kext_bundle.
func blockKextBundle(kexts []*datasets.KextInfo) ([]any, error) {
	out := []any{}
	checked, err := kextChecked(kexts, "IOSkywalkFamily")
	if err != nil {
		return nil, err
	}
	if checked {
		out = append(out, props(
			"Arch", "x86_64",
			"Comment", "Allow IOSkywalk Downgrade",
			"Enabled", true,
			"Identifier", "com.apple.iokit.IOSkywalkFamily",
			"MaxKernel", "",
			"MinKernel", "23.0.0",
			"Strategy", "Exclude",
		))
	}
	return out, nil
}

// spoofedBroadcomPatch is the BCM577XX patch added when CatalinaBCM5701Ethernet is checked.
func spoofedBroadcomPatch() *plist.Dict {
	return props(
		"Arch", "Any",
		"Base", "",
		"Comment", "Broadcom BCM577XX Patch",
		"Count", int64(1),
		"Enabled", true,
		"Find", hexToBytes("E8CA9EFFFF66898300050000"),
		"Identifier", "com.apple.iokit.CatalinaBCM5701Ethernet",
		"Limit", int64(0),
		"Mask", hexToBytes(""),
		"MaxKernel", "",
		"MinKernel", "20.0.0",
		"Replace", hexToBytes("B8B416000066898300050000"),
		"ReplaceMask", hexToBytes(""),
		"Skip", int64(0),
	)
}

// loadKernelPatch mirrors Prodigy.load_kernel_patch.
func (p *Prodigy) loadKernelPatch(chipset, cpuMfr, codename, cores, gpuMfr any, networks any, macosVersion string, kexts []*datasets.KextInfo, provideCurrentCPUInfo bool) ([]any, error) {
	patches := []any{}

	amd, err := pyrt.In("AMD", cpuMfr)
	if err != nil {
		return nil, err
	}
	if amd {
		patches = append(patches, p.KernelPatches("AMD Vanilla Patches", p.AMDVanillaURL)...)
	}

	aquantia, err := anyNetworkMatches(networks, datasets.AquantiaAqtionIDs)
	if err != nil {
		return nil, err
	}
	if aquantia {
		patches = append(patches, p.KernelPatches("Aquantia macOS Patches", p.AquantiaURL)...)
	}

	cpuTopology, err := kextChecked(kexts, "CpuTopologyRebuild")
	if err != nil {
		return nil, err
	}
	forced, err := kextChecked(kexts, "ForgedInvariant")
	if err != nil {
		return nil, err
	}
	catalina, err := kextChecked(kexts, "CatalinaBCM5701Ethernet")
	if err != nil {
		return nil, err
	}
	switch {
	case provideCurrentCPUInfo && cpuTopology:
		patches = append(patches, p.KernelPatches("Hyper Threading Patches", p.HyperThreadingURL)...)
	case forced:
		if !amd {
			list := p.KernelPatches("AMD Vanilla Patches", p.AMDVanillaURL)
			a, b := clampSlice(-6, -4, len(list))
			patches = append(patches, list[a:b]...)
		}
	case catalina:
		patches = append(patches, spoofedBroadcomPatch())
	}

	betaLatest := macos.LatestDarwinVersion(true)
	finalLatest := macos.LatestDarwinVersion(false)

	for _, entry := range patches {
		patch, ok := entry.(*plist.Dict)
		if !ok {
			return nil, fmt.Errorf("TypeError: kernel patch %T is not a dict", entry)
		}
		identifier, err := pyrt.Index(patch, "Identifier")
		if err != nil {
			return nil, err
		}
		comment, err := pyrt.Index(patch, "Comment")
		if err != nil {
			return nil, err
		}
		idInPatch, err := pyrt.In("AppleEthernetAquantiaAqtion", identifier)
		if err != nil {
			return nil, err
		}
		commentHasCores, err := pyrt.In("cpuid_cores_per_package", comment)
		if err != nil {
			return nil, err
		}
		commentHasHotplug, err := pyrt.In("IOPCIIsHotplugPort", comment)
		if err != nil {
			return nil, err
		}
		commentHasMTRR, err := pyrt.In("_mtrr_update_action", comment)
		if err != nil {
			return nil, err
		}
		switch {
		case idInPatch:
			base, err := pyrt.Index(patch, "Base")
			if err != nil {
				return nil, err
			}
			patch.Set("Enabled", !pyrt.Equal(base, ""))
		case commentHasCores:
			replace, err := pyrt.Index(patch, "Replace")
			if err != nil {
				return nil, err
			}
			raw, ok := replace.([]byte)
			if !ok {
				return nil, fmt.Errorf("AttributeError: %T has no attribute 'hex'", replace)
			}
			text := hex.EncodeToString(raw)
			coreHex, err := intToHex(cores)
			if err != nil {
				return nil, err
			}
			patch.Set("Replace", hexToBytes(text[:2]+coreHex+text[4:]))
		case commentHasHotplug:
			amdChipset := inSlice(chipset, datasets.AMDChipsets[min(17, len(datasets.AMDChipsets)):])
			newCPU := inList(codename, "Raphael", "Storm Peak", "Phoenix", "Granite Ridge")
			if amdChipset || newCPU {
				patch.Set("Enabled", true)
			}
		case commentHasMTRR:
			chipsetText, err := pyrt.Str(chipset)
			if err != nil {
				return nil, err
			}
			if strings.Contains(strings.ToUpper(chipsetText), "TRX") {
				patch.Set("Enabled", false)
			} else {
				gpuAMD, err := pyrt.In("AMD", gpuMfr)
				if err != nil {
					return nil, err
				}
				if gpuAMD {
					commentText, err := pyrt.Str(comment)
					if err != nil {
						return nil, err
					}
					lower := strings.ToLower(commentText)
					if strings.Contains(lower, "algrey") {
						patch.Set("Enabled", false)
					} else if strings.Contains(lower, "shaneee") {
						patch.Set("Enabled", true)
					}
				}
			}
		}

		macosName, err := macos.MacOSName(macosVersion)
		if err != nil {
			return nil, err
		}
		if strings.Contains(macosName, "Beta") {
			maxKernel, err := pyrt.Index(patch, "MaxKernel")
			if err != nil {
				return nil, err
			}
			if inList(maxKernel, betaLatest, finalLatest) {
				patch.Set("MaxKernel", betaLatest)
			}
		}
	}
	return patches, nil
}

// anyNetworkMatches mirrors any(network["Device ID"] in list for network in networks.values()).
func anyNetworkMatches(networks any, ids []string) (bool, error) {
	items, err := pyrt.Values(networks)
	if err != nil {
		return false, err
	}
	for _, net := range items {
		deviceID, err := pyrt.Get(net, "Device ID")
		if err != nil {
			return false, err
		}
		if pyrt.InStrings(deviceID, ids) {
			return true, nil
		}
	}
	return false, nil
}

// bootArgs mirrors Prodigy.boot_args.
func (p *Prodigy) bootArgs(report any, macosVersion string, needsOCLP bool, kexts []*datasets.KextInfo, config any) (string, error) {
	args := []string{"-v", "debug=0x100", "keepsyms=1"}

	if needsOCLP {
		ok, err := darwinAtLeast(macosVersion, "25.0.0")
		if err != nil {
			return "", err
		}
		if ok {
			args = append(args, "-amfipassbeta")
		}
	}

	resize, err := index(config, "Booter", "Quirks", "ResizeAppleGpuBars")
	if err != nil {
		return "", err
	}
	if !pyrt.Equal(resize, int64(0)) {
		cpuName, err := get(report, "CPU", "Processor Name")
		if err != nil {
			return "", err
		}
		cpuCodename, err := get(report, "CPU", "Codename")
		if err != nil {
			return "", err
		}
		hedt, err := kextmaestro.IsIntelHEDTCPU(cpuName, cpuCodename)
		if err != nil {
			return "", err
		}
		if hedt {
			args = append(args, "npci=0x2000")
		}
	}

	for _, kext := range kexts {
		if !kext.Checked {
			continue
		}
		if containsString(kext.RequiresKexts, "Lilu") {
			lo, err := cmpDarwin(kext.MinDarwinVersion, macosVersion)
			if err != nil {
				return "", err
			}
			compatible := lo <= 0
			if compatible {
				hi, err := cmpDarwin(macosVersion, kext.MaxDarwinVersion)
				if err != nil {
					return "", err
				}
				compatible = hi <= 0
			}
			if !compatible && !containsString(args, "-lilubetaall") {
				args = append(args, "-lilubetaall")
			}
		}

		switch kext.Name {
		case "WhateverGreen":
			if err := whateverGreenArgs(&args, report, macosVersion); err != nil {
				return "", err
			}
		case "AppleALC":
			layout, _, err := p.selectAudioCodecLayout(report, config, false)
			if err != nil {
				return "", err
			}
			if pyrt.Truthy(layout) {
				n, err := pyrt.IntValue(layout)
				if err != nil {
					return "", err
				}
				args = append(args, fmt.Sprintf("alcid=%d", n))
			}
		case "VoodooI2C":
			args = append(args, "-vi2c-force-polling")
		case "CpuTopologyRebuild":
			args = append(args, "ctrsmt=full")
		}
	}
	return strings.Join(args, " "), nil
}

// containsString mirrors "x in list" for a Go string slice.
func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// csrActiveConfig mirrors Prodigy.csr_active_config.
func csrActiveConfig(macosVersion string) (string, error) {
	atLeast20, err := darwinAtLeast(macosVersion, "20.0.0")
	if err != nil {
		return "", err
	}
	if atLeast20 {
		return "030A0000", nil
	}
	atLeast18, err := darwinAtLeast(macosVersion, "18.0.0")
	if err != nil {
		return "", err
	}
	if atLeast18 {
		return "FF070000", nil
	}
	return "FF030000", nil
}

// loadDrivers mirrors Prodigy.load_drivers.
func loadDrivers(firmware, codename any, macosVersion string, pickerMode any) ([]any, error) {
	hfs := "HfsPlus.efi"
	if inSlice(codename, datasets.IntelCPUGenerations[min(64, len(datasets.IntelCPUGenerations)):]) {
		hfs = "HfsPlusLegacy.efi"
	}
	required := []string{hfs, "OpenRuntime.efi", "ResetNvramEntry.efi"}
	if !pyrt.Equal(firmware, "UEFI") {
		required = append(required, "OpenUsbKbDxe.efi")
	}
	if pyrt.Equal(pickerMode, "External") {
		required = append(required, "OpenCanopy.efi")
	}
	atLeast25, err := darwinAtLeast(macosVersion, "25.0.0")
	if err != nil {
		return nil, err
	}
	if atLeast25 {
		required = append(required, "apfs_aligned.efi")
	}
	sortByLower(required)

	hide := containsString(required, "apfs_aligned.efi")
	out := []any{}
	for _, driver := range required {
		out = append(out, props(
			"Arguments", "",
			"Comment", "",
			"Enabled", true,
			"HideVerbose", hide,
			"LoadEarly", false,
			"Path", driver,
		))
	}
	return out, nil
}

// sortByLower sorts strings by their lowercase form, as sorted(key=str.lower).
func sortByLower(items []string) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && strings.ToLower(items[j]) < strings.ToLower(items[j-1]); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}
