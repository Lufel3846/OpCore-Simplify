package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/datasets"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/kextmaestro"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyrt"
)

// nvramGUID and the other GUID strings are the NVRAM namespaces of the sample.
const (
	nvramAppleBoot = "7C436110-AB2A-4BBB-A880-FE41995C9F82"
	nvramRevision  = "4D1FDA02-38C7-4A6A-9CC6-4BCCA8B30102"
)

// intelSlice and amdSlice mirror chipset_data slices.
func intelSlice(a, b int) []string { return strSlice(datasets.IntelChipsets, a, b) }
func amdSlice(a, b int) []string   { return strSlice(datasets.AMDChipsets, a, b) }

// concat joins string slices, like the list + list used in the original.
func concat(parts ...[]string) []string {
	var out []string
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// matchesSubsystem mirrors device["Subsystem ID"] in table[device["Device ID"]].
func matchesSubsystem(entries []datasets.IDEntry, device any, subsystem any) (bool, error) {
	value, found := datasets.LookupID(entries, device)
	if !found {
		return false, nil
	}
	return pyrt.In(subsystem, value.Value())
}

// anyDeviceSubsystem mirrors any(d.get("Device ID") in table and d.get("Subsystem ID") in table[d.get("Device ID")] for d in System Devices.values()).
func anyDeviceSubsystem(report any, entries []datasets.IDEntry) (bool, error) {
	sysObj, err := pyrt.GetDefault(report, "System Devices", plist.NewDict())
	if err != nil {
		return false, err
	}
	devices, err := pyrt.Values(sysObj)
	if err != nil {
		return false, err
	}
	for _, dev := range devices {
		devID, err := pyrt.Get(dev, "Device ID")
		if err != nil {
			return false, err
		}
		if _, found := datasets.LookupID(entries, devID); !found {
			continue
		}
		subsystem, err := pyrt.Get(dev, "Subsystem ID")
		if err != nil {
			return false, err
		}
		ok, err := matchesSubsystem(entries, devID, subsystem)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// Genarate mirrors ConfigProdigy.genarate. It updates config in place and returns it.
func (p *Prodigy) Genarate(report any, disabled any, smbiosModel, macosVersion string, needsOCLP bool, kexts []*datasets.KextInfo, config *plist.Dict) (*plist.Dict, error) {
	for _, key := range []string{"#WARNING - 1", "#WARNING - 2", "#WARNING - 3", "#WARNING - 4"} {
		if err := deleteKey(config, key); err != nil {
			return nil, err
		}
	}

	board, err := pyrt.Get(report, "Motherboard")
	if err != nil {
		return nil, err
	}
	chipset, err := pyrt.Get(board, "Chipset")
	if err != nil {
		return nil, err
	}
	boardName, err := pyrt.Get(board, "Name")
	if err != nil {
		return nil, err
	}
	platform, err := pyrt.Get(board, "Platform")
	if err != nil {
		return nil, err
	}
	cpu, err := pyrt.Get(report, "CPU")
	if err != nil {
		return nil, err
	}
	cpuMfr, err := pyrt.Get(cpu, "Manufacturer")
	if err != nil {
		return nil, err
	}
	cpuName, err := pyrt.Get(cpu, "Processor Name")
	if err != nil {
		return nil, err
	}
	codename, err := pyrt.Get(cpu, "Codename")
	if err != nil {
		return nil, err
	}
	bios, err := pyrt.Get(report, "BIOS")
	if err != nil {
		return nil, err
	}
	firmware, err := pyrt.Get(bios, "Firmware Type")
	if err != nil {
		return nil, err
	}

	// Booter: MMIO whitelist and booter patches.
	mmio, err := mmioWhitelist(chipset)
	if err != nil {
		return nil, err
	}
	if err := assign(config, mmio, "Booter", "MmioWhitelist"); err != nil {
		return nil, err
	}
	patches, err := addBooterPatch(smbiosModel, macosVersion)
	if err != nil {
		return nil, err
	}
	if len(patches) == 0 {
		patches, err = listAt(config, "Booter", "Patch")
		if err != nil {
			return nil, err
		}
	}
	if err := assign(config, patches, "Booter", "Patch"); err != nil {
		return nil, err
	}

	// Booter quirks.
	legacy := pyrt.Equal(firmware, "Legacy")
	notTwenty := false
	if legacy {
		c, err := cmpDarwin(macosVersion, "20.0.0")
		if err != nil {
			return nil, err
		}
		notTwenty = c != 0
	}
	if err := assign(config, !(legacy && notTwenty), "Booter", "Quirks", "AvoidRuntimeDefrag"); err != nil {
		return nil, err
	}

	devirt, err := devirtualiseMmio(len(mmio), chipset, platform, cpuName)
	if err != nil {
		return nil, err
	}
	if err := assign(config, devirt, "Booter", "Quirks", "DevirtualiseMmio"); err != nil {
		return nil, err
	}

	safeMode := pyrt.Equal(firmware, "UEFI")
	if err := assign(config, safeMode, "Booter", "Quirks", "EnableSafeModeSlide"); err != nil {
		return nil, err
	}

	writeUnprotector, err := writeUnprotectorValue(firmware, cpuMfr, chipset)
	if err != nil {
		return nil, err
	}
	if err := assign(config, writeUnprotector, "Booter", "Quirks", "EnableWriteUnprotector"); err != nil {
		return nil, err
	}

	protect, err := protectMemoryRegions(boardName, report)
	if err != nil {
		return nil, err
	}
	if err := assign(config, protect, "Booter", "Quirks", "ProtectMemoryRegions"); err != nil {
		return nil, err
	}

	fixupEFI, err := fixupAppleEfiImages(firmware, macosVersion)
	if err != nil {
		return nil, err
	}
	if err := assign(config, fixupEFI, "Booter", "Quirks", "FixupAppleEfiImages"); err != nil {
		return nil, err
	}

	protectUEFI, err := protectUefiServices(chipset, cpuName)
	if err != nil {
		return nil, err
	}
	if err := assign(config, protectUEFI, "Booter", "Quirks", "ProtectUefiServices"); err != nil {
		return nil, err
	}

	if err := assign(config, pyrt.Equal(firmware, "UEFI"), "Booter", "Quirks", "ProvideCustomSlide"); err != nil {
		return nil, err
	}

	rebuild, err := rebuildAppleMemoryMap(config, codename, macosVersion)
	if err != nil {
		return nil, err
	}
	if err := assign(config, rebuild, "Booter", "Quirks", "RebuildAppleMemoryMap"); err != nil {
		return nil, err
	}

	resize, err := resizeAppleGpuBars(report)
	if err != nil {
		return nil, err
	}
	if err := assign(config, resize, "Booter", "Quirks", "ResizeAppleGpuBars"); err != nil {
		return nil, err
	}

	setupVirtual, err := setupVirtualMap(firmware, chipset)
	if err != nil {
		return nil, err
	}
	if err := assign(config, setupVirtual, "Booter", "Quirks", "SetupVirtualMap"); err != nil {
		return nil, err
	}

	syncRuntime, err := syncRuntimePermissions(cpuMfr, chipset)
	if err != nil {
		return nil, err
	}
	if err := assign(config, syncRuntime, "Booter", "Quirks", "SyncRuntimePermissions"); err != nil {
		return nil, err
	}

	// Device properties and kernel block.
	deviceProps, err := p.deviceProperties(report, disabled, macosVersion, kexts)
	if err != nil {
		return nil, err
	}
	if err := assign(config, deviceProps, "DeviceProperties", "Add"); err != nil {
		return nil, err
	}

	block, err := blockKextBundle(kexts)
	if err != nil {
		return nil, err
	}
	if err := assign(config, block, "Kernel", "Block"); err != nil {
		return nil, err
	}

	spoof, found, err := spoofCPUID(cpuName, codename, macosVersion)
	if err != nil {
		return nil, err
	}
	if found && spoof != "" {
		if err := assign(config, hexToBytes(spoof+strings.Repeat("0", 24)), "Kernel", "Emulate", "Cpuid1Data"); err != nil {
			return nil, err
		}
		if err := assign(config, hexToBytes(strings.Repeat("F", 8)+strings.Repeat("0", 24)), "Kernel", "Emulate", "Cpuid1Mask"); err != nil {
			return nil, err
		}
	}

	lowEnd, err := isLowEndIntelCPU(cpuName)
	if err != nil {
		return nil, err
	}
	amd, err := pyrt.In("AMD", cpuMfr)
	if err != nil {
		return nil, err
	}
	if err := assign(config, amd || lowEnd, "Kernel", "Emulate", "DummyPowerManagement"); err != nil {
		return nil, err
	}
	if err := assign(config, []any{}, "Kernel", "Force"); err != nil {
		return nil, err
	}

	cpuPmLock := pyrt.InStrings(codename, datasets.IntelCPUGenerations[min(62, len(datasets.IntelCPUGenerations)):])
	if err := assign(config, cpuPmLock, "Kernel", "Quirks", "AppleCpuPmCfgLock"); err != nil {
		return nil, err
	}
	xcpmCfgLock := false
	if !amd {
		current, err := index(config, "Kernel", "Quirks", "AppleCpuPmCfgLock")
		if err != nil {
			return nil, err
		}
		xcpmCfgLock = !pyrt.Truthy(current)
	}
	if err := assign(config, xcpmCfgLock, "Kernel", "Quirks", "AppleXcpmCfgLock"); err != nil {
		return nil, err
	}

	hedt, err := kextmaestro.IsIntelHEDTCPU(cpuName, codename)
	if err != nil {
		return nil, err
	}
	extraMsrs := hedt && pyrt.InStrings(codename, datasets.IntelCPUGenerations[min(50, len(datasets.IntelCPUGenerations)):])
	if err := assign(config, extraMsrs, "Kernel", "Quirks", "AppleXcpmExtraMsrs"); err != nil {
		return nil, err
	}
	if err := assign(config, pyrt.InStrings(codename, datasets.IntelCPUGenerations[:min(4, len(datasets.IntelCPUGenerations))]), "Kernel", "Quirks", "AppleXcpmForceBoost"); err != nil {
		return nil, err
	}
	if err := assign(config, true, "Kernel", "Quirks", "CustomSMBIOSGuid"); err != nil {
		return nil, err
	}
	notAMD, err := pyrt.In("AMD", cpuMfr)
	if err != nil {
		return nil, err
	}
	if err := assign(config, !notAMD, "Kernel", "Quirks", "DisableIoMapper"); err != nil {
		return nil, err
	}
	rtc, err := boardNameHas(boardName, "ASUS", "HP ")
	if err != nil {
		return nil, err
	}
	if err := assign(config, rtc, "Kernel", "Quirks", "DisableRtcChecksum"); err != nil {
		return nil, err
	}
	networksObj, err := pyrt.GetDefault(report, "Network", plist.NewDict())
	if err != nil {
		return nil, err
	}
	aquantia, err := anyNetworkMatches(networksObj, datasets.AquantiaAqtionIDs)
	if err != nil {
		return nil, err
	}
	if err := assign(config, aquantia, "Kernel", "Quirks", "ForceAquantiaEthernet"); err != nil {
		return nil, err
	}
	hp, err := pyrt.In("HP ", boardName)
	if err != nil {
		return nil, err
	}
	if err := assign(config, hp, "Kernel", "Quirks", "LapicKernelPanic"); err != nil {
		return nil, err
	}
	if err := assign(config, true, "Kernel", "Quirks", "PanicNoKextDump"); err != nil {
		return nil, err
	}
	if err := assign(config, true, "Kernel", "Quirks", "PowerTimeoutKernelPanic"); err != nil {
		return nil, err
	}
	provideInfo := amd || pyrt.InStrings(codename, datasets.IntelCPUGenerations[min(4, len(datasets.IntelCPUGenerations)):min(20, len(datasets.IntelCPUGenerations))])
	if err := assign(config, provideInfo, "Kernel", "Quirks", "ProvideCurrentCpuInfo"); err != nil {
		return nil, err
	}

	// Kernel patches: arguments are read in the order of the call.
	cores, err := pyrt.Get(cpu, "Core Count")
	if err != nil {
		return nil, err
	}
	firstGPUEntry, err := firstGPU(report)
	if err != nil {
		return nil, err
	}
	gpuMfr, err := pyrt.Get(firstGPUEntry.Value, "Manufacturer")
	if err != nil {
		return nil, err
	}
	networks, err := pyrt.GetDefault(report, "Network", plist.NewDict())
	if err != nil {
		return nil, err
	}
	provide, err := index(config, "Kernel", "Quirks", "ProvideCurrentCpuInfo")
	if err != nil {
		return nil, err
	}
	kernelPatch, err := p.loadKernelPatch(chipset, cpuMfr, codename, cores, gpuMfr, networks, macosVersion, kexts, pyrt.Truthy(provide))
	if err != nil {
		return nil, err
	}
	if err := assign(config, kernelPatch, "Kernel", "Patch"); err != nil {
		return nil, err
	}

	// Misc.
	steps := []struct {
		value any
		keys  []string
	}{
		{[]any{}, []string{"Misc", "BlessOverride"}},
		{"Builtin", []string{"Misc", "Boot", "PickerMode"}},
		{false, []string{"Misc", "Debug", "AppleDebug"}},
		{false, []string{"Misc", "Debug", "ApplePanic"}},
		{true, []string{"Misc", "Debug", "DisableWatchDog"}},
		{[]any{}, []string{"Misc", "Entries"}},
		{true, []string{"Misc", "Security", "AllowSetDefault"}},
		{int64(0), []string{"Misc", "Security", "ScanPolicy"}},
	}
	for _, s := range steps {
		if err := assign(config, s.value, s.keys...); err != nil {
			return nil, err
		}
	}
	secureBoot := "Disabled"
	if !needsOCLP {
		inRange, err := darwinBetween("20.0.0", macosVersion, "23.0.0")
		if err != nil {
			return nil, err
		}
		if inRange {
			secureBoot = "Default"
		}
	}
	if err := assign(config, secureBoot, "Misc", "Security", "SecureBootModel"); err != nil {
		return nil, err
	}
	if err := assign(config, "Optional", "Misc", "Security", "Vault"); err != nil {
		return nil, err
	}
	if err := assign(config, []any{}, "Misc", "Tools"); err != nil {
		return nil, err
	}

	// NVRAM.
	if err := deleteKey(config, "NVRAM", "Add", nvramAppleBoot, "#INFO (prev-lang:kbd)"); err != nil {
		return nil, err
	}
	bootArgs, err := p.bootArgs(report, macosVersion, needsOCLP, kexts, config)
	if err != nil {
		return nil, err
	}
	if err := assign(config, bootArgs, "NVRAM", "Add", nvramAppleBoot, "boot-args"); err != nil {
		return nil, err
	}
	csr, err := csrActiveConfig(macosVersion)
	if err != nil {
		return nil, err
	}
	if err := assign(config, hexToBytes(csr), "NVRAM", "Add", nvramAppleBoot, "csr-active-config"); err != nil {
		return nil, err
	}
	if err := appendList(config, "csr-active-config", "NVRAM", "Delete", nvramAppleBoot); err != nil {
		return nil, err
	}
	if err := assign(config, []byte{}, "NVRAM", "Add", nvramAppleBoot, "prev-lang:kbd"); err != nil {
		return nil, err
	}

	// PlatformInfo: serial data from the SMBIOS generator.
	if p.GenerateSMBIOS == nil {
		return nil, ErrSerialNotPorted
	}
	serial, err := p.GenerateSMBIOS(smbiosModel)
	if err != nil {
		return nil, err
	}
	generic, err := index(config, "PlatformInfo", "Generic")
	if err != nil {
		return nil, err
	}
	genericDict, ok := generic.(*plist.Dict)
	if !ok {
		return nil, fmt.Errorf("TypeError: PlatformInfo Generic is not a dict")
	}
	keys := make([]string, 0, len(serial))
	for k := range serial {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		genericDict.Set(k, serial[k])
	}
	romValue, err := pyrt.Get(generic, "ROM")
	if err != nil {
		return nil, err
	}
	if err := assign(config, hexToBytes(fmt.Sprint(romValue)), "PlatformInfo", "Generic", "ROM"); err != nil {
		return nil, err
	}
	if err := assign(config, "Custom", "PlatformInfo", "UpdateSMBIOSMode"); err != nil {
		return nil, err
	}

	// UEFI.
	atLeast25, err := darwinAtLeast(macosVersion, "25.0.0")
	if err != nil {
		return nil, err
	}
	if atLeast25 {
		if err := assign(config, false, "UEFI", "APFS", "EnableJumpstart"); err != nil {
			return nil, err
		}
	}
	if err := assign(config, int64(-1), "UEFI", "APFS", "MinDate"); err != nil {
		return nil, err
	}
	if err := assign(config, int64(-1), "UEFI", "APFS", "MinVersion"); err != nil {
		return nil, err
	}
	pickerMode, err := index(config, "Misc", "Boot", "PickerMode")
	if err != nil {
		return nil, err
	}
	drivers, err := loadDrivers(firmware, codename, macosVersion, pickerMode)
	if err != nil {
		return nil, err
	}
	if err := assign(config, drivers, "UEFI", "Drivers"); err != nil {
		return nil, err
	}
	if err := assign(config, pyrt.Equal(firmware, "UEFI"), "UEFI", "Input", "KeySupport"); err != nil {
		return nil, err
	}
	thinkpad, err := anyDeviceSubsystem(report, datasets.ThinkPadTWX30IDs)
	if err != nil {
		return nil, err
	}
	if err := assign(config, thinkpad, "UEFI", "Quirks", "ForceOcWriteFlash"); err != nil {
		return nil, err
	}
	if err := assign(config, !pyrt.InStrings(codename, datasets.IntelCPUGenerations[:min(20, len(datasets.IntelCPUGenerations))]), "UEFI", "Quirks", "EnableVectorAcceleration"); err != nil {
		return nil, err
	}
	if err := assign(config, pyrt.InStrings(codename, datasets.IntelCPUGenerations[min(50, len(datasets.IntelCPUGenerations)):]), "UEFI", "Quirks", "IgnoreInvalidFlexRatio"); err != nil {
		return nil, err
	}
	if err := assign(config, true, "UEFI", "Quirks", "ReleaseUsbOwnership"); err != nil {
		return nil, err
	}
	if err := assign(config, hp, "UEFI", "Quirks", "UnblockFsConnect"); err != nil {
		return nil, err
	}
	if err := assign(config, []any{}, "UEFI", "ReservedMemory"); err != nil {
		return nil, err
	}

	bluetooth, err := kextChecked(kexts, "BlueToolFixup")
	if err != nil {
		return nil, err
	}
	if bluetooth {
		if err := assign(config, []byte{0x00}, "NVRAM", "Add", nvramAppleBoot, "bluetoothExternalDongleFailed"); err != nil {
			return nil, err
		}
		if err := assign(config, hexToBytes(strings.Repeat("0", 28)), "NVRAM", "Add", nvramAppleBoot, "bluetoothInternalControllerInfo"); err != nil {
			return nil, err
		}
		for _, name := range []string{"bluetoothExternalDongleFailed", "bluetoothInternalControllerInfo"} {
			if err := appendList(config, name, "NVRAM", "Delete", nvramAppleBoot); err != nil {
				return nil, err
			}
		}
	}

	restrict, err := kextChecked(kexts, "RestrictEvents")
	if err != nil {
		return nil, err
	}
	if restrict {
		if err := restrictEvents(config, report, macosVersion, cpuName, codename, cpuMfr, chipset); err != nil {
			return nil, err
		}
	}

	nvramRevisionDict, err := index(config, "NVRAM", "Add", nvramRevision)
	if err != nil {
		return nil, err
	}
	revisionKeys := nvramRevisionDict.(*plist.Dict).Keys()
	deleteList := make([]any, 0, len(revisionKeys))
	for _, k := range revisionKeys {
		deleteList = append(deleteList, k)
	}
	if err := assign(config, deleteList, "NVRAM", "Delete", nvramRevision); err != nil {
		return nil, err
	}
	return config, nil
}

// devirtualiseMmio mirrors the DevirtualiseMmio expression of genarate.
func devirtualiseMmio(mmioCount int, chipset, platform, cpuName any) (bool, error) {
	if mmioCount != 0 {
		return true, nil
	}
	if inSlice(chipset, concat(intelSlice(112, len(datasets.IntelChipsets)), intelSlice(90, 100))) {
		return true, nil
	}
	if inSlice(chipset, intelSlice(104, 112)) {
		desktop, err := pyrt.In("Desktop", platform)
		if err != nil {
			return false, err
		}
		if desktop {
			return true, nil
		}
		eight, err := pyrt.In("-8", cpuName)
		if err != nil {
			return false, err
		}
		if !eight {
			return true, nil
		}
	}
	return pyrt.Equal(chipset, datasets.AMDChipsets[16]), nil
}

// writeUnprotectorValue mirrors EnableWriteUnprotector.
func writeUnprotectorValue(firmware, cpuMfr, chipset any) (bool, error) {
	if pyrt.Equal(firmware, "Legacy") {
		return false, nil
	}
	amd, err := pyrt.In("AMD", cpuMfr)
	if err != nil {
		return false, err
	}
	if amd {
		return false, nil
	}
	return !inSlice(chipset, concat(intelSlice(91, 100), intelSlice(112, len(datasets.IntelChipsets)))), nil
}

// protectMemoryRegions mirrors ProtectMemoryRegions.
func protectMemoryRegions(boardName, report any) (bool, error) {
	google, err := pyrt.In("GOOGLE", boardName)
	if err != nil {
		return false, err
	}
	if google {
		return true, nil
	}
	return anyDeviceSubsystem(report, datasets.ChromebookIDs)
}

// fixupAppleEfiImages mirrors FixupAppleEfiImages.
func fixupAppleEfiImages(firmware any, macosVersion string) (bool, error) {
	if !pyrt.Equal(firmware, "Legacy") {
		return true, nil
	}
	c, err := cmpDarwin("17.0.0", macosVersion)
	if err != nil {
		return false, err
	}
	return !(c < 0), nil
}

// protectUefiServices mirrors ProtectUefiServices.
func protectUefiServices(chipset, cpuName any) (bool, error) {
	if inSlice(chipset, intelSlice(112, len(datasets.IntelChipsets))) {
		return true, nil
	}
	if !inSlice(chipset, intelSlice(104, 112)) {
		return false, nil
	}
	eight, err := pyrt.In("-8", cpuName)
	if err != nil {
		return false, err
	}
	if !eight {
		return true, nil
	}
	return pyrt.Equal(chipset, datasets.IntelChipsets[109]), nil
}

// rebuildAppleMemoryMap mirrors RebuildAppleMemoryMap.
func rebuildAppleMemoryMap(config *plist.Dict, codename any, macosVersion string) (bool, error) {
	if !pyrt.InStrings(codename, datasets.IntelCPUGenerations[max(0, len(datasets.IntelCPUGenerations)-11):]) {
		current, err := index(config, "Booter", "Quirks", "EnableWriteUnprotector")
		if err != nil {
			return false, err
		}
		return !pyrt.Truthy(current), nil
	}
	c, err := cmpDarwin(macosVersion, "10.0.0")
	if err != nil {
		return false, err
	}
	return c < 0, nil
}

// resizeAppleGpuBars mirrors ResizeAppleGpuBars: 0 when a GPU has resizable BAR
// enabled, otherwise -1.
func resizeAppleGpuBars(report any) (int64, error) {
	gpuObj, err := pyrt.GetDefault(report, "GPU", plist.NewDict())
	if err != nil {
		return 0, err
	}
	items, err := pyrt.Items(gpuObj)
	if err != nil {
		return 0, err
	}
	for _, gpu := range items {
		bar, err := pyrt.GetDefault(gpu.Value, "Resizable BAR", "Disabled")
		if err != nil {
			return 0, err
		}
		if pyrt.Equal(bar, "Enabled") {
			return 0, nil
		}
	}
	return -1, nil
}

// setupVirtualMap mirrors SetupVirtualMap.
func setupVirtualMap(firmware, chipset any) (bool, error) {
	if !pyrt.Equal(firmware, "UEFI") {
		return false, nil
	}
	return !inSlice(chipset, concat(amdSlice(11, 17), intelSlice(90, 100))), nil
}

// syncRuntimePermissions mirrors SyncRuntimePermissions.
func syncRuntimePermissions(cpuMfr, chipset any) (bool, error) {
	amd, err := pyrt.In("AMD", cpuMfr)
	if err != nil {
		return false, err
	}
	if amd {
		return true, nil
	}
	return inSlice(chipset, concat(intelSlice(90, 100), intelSlice(104, len(datasets.IntelChipsets)))), nil
}

// boardNameHas reports whether the board name contains any of the given parts.
func boardNameHas(boardName any, parts ...string) (bool, error) {
	for _, p := range parts {
		in, err := pyrt.In(p, boardName)
		if err != nil {
			return false, err
		}
		if in {
			return true, nil
		}
	}
	return false, nil
}

// restrictEvents is the RestrictEvents block of genarate.
func restrictEvents(config *plist.Dict, report any, macosVersion string, cpuName, codename, cpuMfr, chipset any) error {
	var revpatch, revblock []string

	above23, err := cmpDarwin(macosVersion, "23.0.0")
	if err != nil {
		return err
	}
	if above23 > 0 {
		revpatch = append(revpatch, "sbvmm")
	} else {
		patchList, err := listAt(config, "Booter", "Patch")
		if err != nil {
			return err
		}
		if len(patchList) != 0 {
			above204, err := cmpDarwin(macosVersion, "20.4.0")
			if err != nil {
				return err
			}
			if above204 > 0 {
				revpatch = append(revpatch, "sbvmm")
			}
		}
	}

	core, err := pyrt.In(" Core", cpuName)
	if err != nil {
		return err
	}
	coreInner := core && pyrt.InStrings(codename, datasets.IntelCPUGenerations[min(28, len(datasets.IntelCPUGenerations)):])
	if !coreInner {
		if err := assign(config, int64(1), "NVRAM", "Add", nvramRevision, "revcpu"); err != nil {
			return err
		}
		if err := assign(config, cpuName, "NVRAM", "Add", nvramRevision, "revcpuname"); err != nil {
			return err
		}
		if above23 > 0 {
			revpatch = append(revpatch, "cpuname")
		}
		coreCount, err := pyrt.Get(report, "CPU")
		if err != nil {
			return err
		}
		cores, err := pyrt.Get(coreCount, "Core Count")
		if err != nil {
			return err
		}
		n, err := pyrt.IntValue(cores)
		if err != nil {
			return err
		}
		processorType := int64(3841)
		if n < 8 {
			processorType = 1537
		}
		if err := assign(config, processorType, "PlatformInfo", "Generic", "ProcessorType"); err != nil {
			return err
		}
	}

	intel, err := pyrt.In("Intel", cpuMfr)
	if err != nil {
		return err
	}
	if intel {
		igpuEntry, err := lastGPU(report)
		if err != nil {
			return err
		}
		igpuType, err := pyrt.Get(igpuEntry.Value, "Device Type")
		if err != nil {
			return err
		}
		integrated, err := pyrt.In("Integrated GPU", igpuType)
		if err != nil {
			return err
		}
		if integrated {
			compat, err := pyrt.Get(igpuEntry.Value, "OCLP Compatibility")
			if err != nil {
				return err
			}
			if pyrt.Truthy(compat) {
				if err := assign(config, "-allow_amfi", "NVRAM", "Add", nvramRevision, "OCLP-Settings"); err != nil {
					return err
				}
				atLeast204, err := darwinAtLeast(macosVersion, "20.4.0")
				if err != nil {
					return err
				}
				if atLeast204 {
					gen, err := pyrt.Get(igpuEntry.Value, "Codename")
					if err != nil {
						return err
					}
					if inList(gen, "Broadwell", "Haswell", "Ivy Bridge", "Sandy Bridge") {
						revblock = append(revblock, "media")
					}
					if inList(gen, "Kaby Lake", "Skylake", "Broadwell", "Haswell") {
						revpatch = append(revpatch, "asset")
					} else if inList(gen, "Ivy Bridge", "Sandy Bridge") {
						revpatch = append(revpatch, "f16c")
					}
				}
			}
		}
	}

	if len(revpatch) > 0 {
		if err := assign(config, joinComma(revpatch), "NVRAM", "Add", nvramRevision, "revpatch"); err != nil {
			return err
		}
	}
	if len(revblock) > 0 {
		if err := assign(config, joinComma(revblock), "NVRAM", "Add", nvramRevision, "revblock"); err != nil {
			return err
		}
	}
	return nil
}

func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ","
		}
		out += p
	}
	return out
}
