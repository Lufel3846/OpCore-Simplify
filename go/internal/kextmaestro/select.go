package kextmaestro

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/datasets"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/macos"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyrt"
)

// chain mirrors obj.get(k1).get(k2)... on the hardware report.
func chain(obj any, keys ...string) (any, error) {
	cur := obj
	for _, k := range keys {
		next, err := pyrt.Get(cur, k)
		if err != nil {
			return nil, err
		}
		cur = next
	}
	return cur, nil
}

// codecLayoutHas mirrors "Device ID in codec_layouts.data" for a dict of layouts.
func codecLayoutHas(v any) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	_, found := datasets.CodecLayouts[s]
	return found
}

// isDigits mirrors str.isdigit() for ASCII digits. The input must be non-empty.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// darwinAtLeast reports parse(macos) >= parse(limit).
func darwinAtLeast(current, limit string) (bool, error) {
	c, err := cmpVersion(current, limit)
	if err != nil {
		return false, err
	}
	return c >= 0, nil
}

// SelectRequiredKexts mirrors select_required_kexts. report is the hardware
// report (a *plist.Dict from the JSON report); it returns needs_oclp, which may
// become true when the user picks an audio or Wi-Fi option that needs root patches.
func (m *Maestro) SelectRequiredKexts(report any, macosVersion string, needsOCLP bool, acpiPatches []ACPIPatch) (bool, error) {
	m.head("Select Required Kernel Extensions", 68, true)
	m.logf("\nChecking for required kernel extensions...\n")
	for _, k := range m.Kexts {
		k.Checked = k.Required
	}

	selected := []string{"UTBDefault"}

	cpu, err := chain(report, "CPU")
	if err != nil {
		return false, err
	}
	mb, err := chain(report, "Motherboard")
	if err != nil {
		return false, err
	}

	manufacturer, err := pyrt.Get(cpu, "Manufacturer")
	if err != nil {
		return false, err
	}
	if in, err := pyrt.In("Intel", manufacturer); err != nil {
		return false, err
	} else if in {
		selected = append(selected, "SMCProcessor", "SMCSuperIO")
	}

	platform, err := pyrt.Get(mb, "Platform")
	if err != nil {
		return false, err
	}
	name, err := pyrt.Get(mb, "Name")
	if err != nil {
		return false, err
	}
	laptop, err := pyrt.In("Laptop", platform)
	if err != nil {
		return false, err
	}
	if laptop {
		surface, err := pyrt.In("SURFACE", name)
		if err != nil {
			return false, err
		}
		if !surface {
			selected = append(selected, "SMCBatteryManager")
			dell, err := pyrt.In("DELL", name)
			if err != nil {
				return false, err
			}
			if dell {
				selected = append(selected, "SMCDellSensors")
			}
			selected = append(selected, "SMCLightSensor")
		}
	}

	// not (" Core" in name and codename in IntelCPUGenerations[28:]) or darwin >= 23.0.0
	processorName, err := pyrt.Get(cpu, "Processor Name")
	if err != nil {
		return false, err
	}
	codename, err := pyrt.Get(cpu, "Codename")
	if err != nil {
		return false, err
	}
	inner := false
	if core, err := pyrt.In(" Core", processorName); err != nil {
		return false, err
	} else if core {
		inner = pyrt.InStrings(codename, datasets.IntelCPUGenerations[min(28, len(datasets.IntelCPUGenerations)):])
	}
	if !inner {
		selected = append(selected, "RestrictEvents")
	} else {
		atLeast, err := darwinAtLeast(macosVersion, "23.0.0")
		if err != nil {
			return false, err
		}
		if atLeast {
			selected = append(selected, "RestrictEvents")
		}
	}

	// Audio: any codec of the report whose Device ID is in codec_layouts.data.
	soundObj, err := pyrt.GetDefault(report, "Sound", plist.NewDict())
	if err != nil {
		return false, err
	}
	sounds, err := pyrt.Values(soundObj)
	if err != nil {
		return false, err
	}
	anyCodec := false
	for _, codec := range sounds {
		deviceID, err := pyrt.Get(codec, "Device ID")
		if err != nil {
			return false, err
		}
		if codecLayoutHas(deviceID) {
			anyCodec = true
			break
		}
	}
	if anyCodec {
		tahoe, err := darwinAtLeast(macosVersion, "25.0.0")
		if err != nil {
			return false, err
		}
		if tahoe {
			for {
				answer, err := m.input("Select audio kext for your system: ")
				if err != nil {
					return false, err
				}
				answer = strings.TrimSpace(answer)
				if answer == "1" {
					needsOCLP = true
					selected = append(selected, "AppleALC")
					break
				} else if answer == "2" {
					selected = append(selected, "AppleALC", "SimpleHDA")
					break
				}
			}
		} else {
			selected = append(selected, "AppleALC")
		}
	}

	// ("AMD" in manufacturer and darwin >= 21.4.0)
	//   or (int(CPU Count) > 1 and darwin >= 19.0.0)
	//   or codename in IntelCPUGenerations[:4]
	needMCE := false
	amd, err := pyrt.In("AMD", manufacturer)
	if err != nil {
		return false, err
	}
	if amd {
		ok, err := darwinAtLeast(macosVersion, "21.4.0")
		if err != nil {
			return false, err
		}
		needMCE = ok
	}
	if !needMCE {
		cpuCountObj, err := pyrt.Get(cpu, "CPU Count")
		if err != nil {
			return false, err
		}
		count, err := pyrt.IntValue(cpuCountObj)
		if err != nil {
			return false, err
		}
		if count > 1 {
			ok, err := darwinAtLeast(macosVersion, "19.0.0")
			if err != nil {
				return false, err
			}
			needMCE = ok
		}
	}
	if !needMCE {
		needMCE = pyrt.InStrings(codename, datasets.IntelCPUGenerations[:min(4, len(datasets.IntelCPUGenerations))])
	}
	if needMCE {
		selected = append(selected, "AppleMCEReporterDisabler")
	}

	// darwin >= 22.0.0 and not "AVX2" in SIMD Features
	if atLeast, err := darwinAtLeast(macosVersion, "22.0.0"); err != nil {
		return false, err
	} else if atLeast {
		simd, err := pyrt.Get(cpu, "SIMD Features")
		if err != nil {
			return false, err
		}
		hasAVX2, err := pyrt.In("AVX2", simd)
		if err != nil {
			return false, err
		}
		if !hasAVX2 {
			selected = append(selected, "CryptexFixup")
		}
	}

	// "Lunar Lake" not in codename and "Meteor Lake" not in codename
	//   and codename in IntelCPUGenerations[:20] and int(Core Count) > 6
	lunar, err := pyrt.In("Lunar Lake", codename)
	if err != nil {
		return false, err
	}
	if !lunar {
		meteor, err := pyrt.In("Meteor Lake", codename)
		if err != nil {
			return false, err
		}
		if !meteor && pyrt.InStrings(codename, datasets.IntelCPUGenerations[:min(20, len(datasets.IntelCPUGenerations))]) {
			coreObj, err := pyrt.Get(cpu, "Core Count")
			if err != nil {
				return false, err
			}
			cores, err := pyrt.IntValue(coreObj)
			if err != nil {
				return false, err
			}
			if cores > 6 {
				selected = append(selected, "CpuTopologyRebuild")
			}
		}
	}

	gpuObj, err := pyrt.GetDefault(report, "GPU", plist.NewDict())
	if err != nil {
		return false, err
	}
	gpus, err := pyrt.Items(gpuObj)
	if err != nil {
		return false, err
	}
	for _, gpu := range gpus {
		deviceType, err := pyrt.Get(gpu.Value, "Device Type")
		if err != nil {
			return false, err
		}
		integrated, err := pyrt.In("Integrated GPU", deviceType)
		if err != nil {
			return false, err
		}
		if integrated {
			gpuMfr, err := pyrt.Get(gpu.Value, "Manufacturer")
			if err != nil {
				return false, err
			}
			amdGPU, err := pyrt.In("AMD", gpuMfr)
			if err != nil {
				return false, err
			}
			if amdGPU {
				selected = append(selected, "NootedRed")
			} else {
				selected = append(selected, "WhateverGreen")
			}
			continue
		}

		gpuCodename, err := pyrt.Get(gpu.Value, "Codename")
		if err != nil {
			return false, err
		}
		navi22, err := pyrt.In("Navi 22", gpuCodename)
		if err != nil {
			return false, err
		}
		if navi22 {
			selected = append(selected, "NootRX")
			break
		}

		if s, ok := gpuCodename.(string); ok && (s == "Navi 21" || s == "Navi 23") {
			intelPresent := false
			for _, other := range gpus {
				otherMfr, err := pyrt.Get(other.Value, "Manufacturer")
				if err != nil {
					return false, err
				}
				if pyrt.Equal(otherMfr, "Intel") {
					intelPresent = true
					break
				}
			}
			if intelPresent {
				if _, err := m.input("Press Enter to continue..."); err != nil {
					return false, err
				}
				continue
			}

			answer, err := m.input(fmt.Sprintf("Select kext for your AMD %s GPU (default: WhateverGreen): ", s))
			if err != nil {
				return false, err
			}
			option := strings.TrimSpace(answer)
			if option == "" {
				option = "2"
			}
			selectedOption := 2
			if isDigits(option) {
				n, _ := strconv.Atoi(option)
				if n > 0 && n < 3 {
					selectedOption = n
				}
			}
			if selectedOption == 1 {
				selected = append(selected, "NootRX")
			} else if selectedOption == 2 {
				selected = append(selected, "WhateverGreen")
			}
			continue
		}

		selected = append(selected, "WhateverGreen")
	}

	// "Laptop" in platform and ("ASUS" in name or "NootedRed" in selected)
	if laptop {
		asus, err := pyrt.In("ASUS", name)
		if err != nil {
			return false, err
		}
		if asus || containsString(selected, "NootedRed") {
			selected = append(selected, "ForgedInvariant")
		}
	}

	processorForHEDT, err := pyrt.Get(cpu, "Processor Name")
	if err != nil {
		return false, err
	}
	codenameForHEDT, err := pyrt.Get(cpu, "Codename")
	if err != nil {
		return false, err
	}
	hedt, err := IsIntelHEDTCPU(processorForHEDT, codenameForHEDT)
	if err != nil {
		return false, err
	}
	if hedt {
		selected = append(selected, "CpuTscSync")
	}

	networkObj, err := pyrt.GetDefault(report, "Network", plist.NewDict())
	if err != nil {
		return false, err
	}
	networks, err := pyrt.Items(networkObj)
	if err != nil {
		return false, err
	}
	for _, network := range networks {
		deviceID, err := pyrt.Get(network.Value, "Device ID")
		if err != nil {
			return false, err
		}
		selected, needsOCLP, err = m.selectWifi(selected, deviceID, network.Key, macosVersion, needsOCLP)
		if err != nil {
			return false, err
		}
	}

	allUSB := true
	for _, network := range networks {
		busType, err := pyrt.Get(network.Value, "Bus Type")
		if err != nil {
			return false, err
		}
		if !pyrt.Equal(busType, "USB") {
			allUSB = false
			break
		}
	}
	if allUSB {
		selected = append(selected, "NullEthernet")
	}

	bluetoothObj, err := pyrt.GetDefault(report, "Bluetooth", plist.NewDict())
	if err != nil {
		return false, err
	}
	bluetooths, err := pyrt.Items(bluetoothObj)
	if err != nil {
		return false, err
	}
	for _, bt := range bluetooths {
		usbID, err := pyrt.Get(bt.Value, "Device ID")
		if err != nil {
			return false, err
		}
		switch {
		case pyrt.InStrings(usbID, datasets.AtherosBluetoothIDs):
			selected = append(selected, "Ath3kBT", "Ath3kBTInjector")
		case pyrt.InStrings(usbID, datasets.BroadcomBluetoothIDs):
			selected = append(selected, "BrcmFirmwareData", "BrcmPatchRAM2", "BrcmPatchRAM3", "BrcmBluetoothInjector", "BlueToolFixup")
		case pyrt.InStrings(usbID, datasets.IntelBluetoothIDs):
			selected = append(selected, "IntelBluetoothFirmware", "IntelBTPatcher", "IntelBluetoothInjector", "BlueToolFixup")
		case pyrt.InStrings(usbID, datasets.RealtekBluetoothIDs):
			selected = append(selected, "RealtekBluetoothFirmware", "BlueToolFixup")
		default:
			in, err := pyrt.In(usbID, datasets.BluetoothIDs[len(datasets.BluetoothIDs)-1])
			if err != nil {
				return false, err
			}
			if in {
				selected = append(selected, "BlueToolFixup")
			}
		}
	}

	if laptop {
		surface, err := pyrt.In("SURFACE", name)
		if err != nil {
			return false, err
		}
		if surface {
			selected = append(selected, "BigSurface")
		} else {
			asus, err := pyrt.In("ASUS", name)
			if err != nil {
				return false, err
			}
			if asus {
				selected = append(selected, "AsusSMC")
			}
			selected = append(selected, "BrightnessKeys")

			inputObj, err := pyrt.Get(report, "Input")
			if err != nil {
				return false, err
			}
			inputs, err := pyrt.Items(inputObj)
			if err != nil {
				return false, err
			}
			for _, dev := range inputs {
				deviceVal, err := pyrt.Get(dev.Value, "Device")
				if err != nil {
					return false, err
				}
				if !pyrt.Truthy(deviceVal) {
					continue
				}
				idx := -1
				for i, v := range datasets.InputIDs {
					if pyrt.Equal(deviceVal, v) {
						idx = i
						break
					}
				}
				deviceType, err := pyrt.GetDefault(dev.Value, "Device Type", "None")
				if err != nil {
					return false, err
				}
				ps2, err := pyrt.In("PS/2", deviceType)
				if err != nil {
					return false, err
				}
				if ps2 {
					selected = append(selected, "VoodooPS2Controller")
					deviceID, err := pyrt.Str(deviceVal)
					if err != nil {
						return false, err
					}
					if strings.HasPrefix(deviceID, "SYN") {
						selected = append(selected, "VoodooRMI")
					} else if idx > 0 && 75 < idx && idx < 79 {
						selected = append(selected, "VoodooSMBus")
					}
				}
				i2c, err := pyrt.In("I2C", deviceType)
				if err != nil {
					return false, err
				}
				if i2c {
					selected = append(selected, "VoodooI2CHID")
					if idx > 0 {
						if idx < 76 {
							selected = append(selected, "AlpsHID")
						} else if 78 < idx {
							selected = append(selected, "VoodooRMI")
						}
					}
				}
			}
		}
	}

	systemObj, err := pyrt.GetDefault(report, "System Devices", plist.NewDict())
	if err != nil {
		return false, err
	}
	systemDevices, err := pyrt.Items(systemObj)
	if err != nil {
		return false, err
	}
	for _, dev := range systemDevices {
		busType, err := pyrt.Get(dev.Value, "Bus Type")
		if err != nil {
			return false, err
		}
		if !pyrt.Equal(busType, "ACPI") {
			continue
		}
		deviceVal, err := pyrt.Get(dev.Value, "Device")
		if err != nil {
			return false, err
		}
		if pyrt.InStrings(deviceVal, datasets.YogaHIDs) {
			selected = append(selected, "YogaSMC")
		}
	}

	for _, patch := range acpiPatches {
		if patch.Name == "BATP" && patch.Checked {
			selected = append(selected, "ECEnabler")
			break
		}
	}

	sdObj, err := pyrt.GetDefault(report, "SD Controller", plist.NewDict())
	if err != nil {
		return false, err
	}
	sdControllers, err := pyrt.Items(sdObj)
	if err != nil {
		return false, err
	}
	for _, c := range sdControllers {
		deviceID, err := pyrt.Get(c.Value, "Device ID")
		if err != nil {
			return false, err
		}
		if pyrt.InStrings(deviceID, datasets.RealtekCardReaderIDs) {
			if pyrt.InStrings(deviceID, strSlice(datasets.RealtekCardReaderIDs, 5, len(datasets.RealtekCardReaderIDs))) {
				selected = append(selected, "Sinetek-rtsx")
			} else {
				selected = append(selected, "RealtekCardReader")
			}
		}
	}

	storageObj, err := pyrt.GetDefault(report, "Storage Controllers", plist.NewDict())
	if err != nil {
		return false, err
	}
	storages, err := pyrt.Items(storageObj)
	if err != nil {
		return false, err
	}
	for _, c := range storages {
		deviceID, err := pyrt.Get(c.Value, "Device ID")
		if err != nil {
			return false, err
		}
		if pyrt.Equal(deviceID, "1C5C-174A") {
			selected = append(selected, "PC711Probe")
		}

		nvme := strings.Contains(c.Key, "NVMe") || strings.Contains(c.Key, "NVM Express")
		if nvme {
			selected = append(selected, "NVMeFix")
			continue
		}
		notAHCI := !strings.Contains(c.Key, "AHCI")
		amdCPU := false
		if !notAHCI {
			amdCPU, err = pyrt.In("AMD", manufacturer)
			if err != nil {
				return false, err
			}
		}
		if notAHCI || amdCPU {
			atLeast, err := darwinAtLeast(macosVersion, "20.0.0")
			if err != nil {
				return false, err
			}
			if atLeast {
				if pyrt.InStrings(deviceID, datasets.UnsupportedSATAControllerIDs) {
					selected = append(selected, "CtlnaAHCIPort")
				}
			} else if pyrt.InStrings(deviceID, strSlice(datasets.UnsupportedSATAControllerIDs, 15, len(datasets.UnsupportedSATAControllerIDs))) {
				selected = append(selected, "SATA-unsupported")
			}
		}
	}

	usbObj, err := pyrt.Get(report, "USB Controllers")
	if err != nil {
		return false, err
	}
	usbControllers, err := pyrt.Items(usbObj)
	if err != nil {
		return false, err
	}
	for _, c := range usbControllers {
		deviceID, err := pyrt.Get(c.Value, "Device ID")
		if err != nil {
			return false, err
		}
		subsystemDefault, err := pyrt.GetDefault(c.Value, "Subsystem ID", strings.Repeat("0", 8))
		if err != nil {
			return false, err
		}
		var unsupported *datasets.USBControllerEntry
		for i := range datasets.UnsupportedUSBControllerIDs {
			entry := &datasets.UnsupportedUSBControllerIDs[i]
			if pyrt.Equal(deviceID, entry.Device) {
				unsupported = entry
				break
			}
		}
		if unsupported == nil {
			continue
		}
		subsystemIDs := unsupported.Subsystem.Value()
		n, err := pyrt.Len(subsystemIDs)
		if err != nil {
			return false, err
		}
		if n > 0 {
			in, err := pyrt.In(subsystemDefault, subsystemIDs)
			if err != nil {
				return false, err
			}
			if !in {
				continue
			}
			// "subsystem_id not in subsystem_ids": skip when it is absent.
		}
		deviceStr, err := pyrt.Str(deviceID)
		if err != nil {
			return false, err
		}
		if strings.HasPrefix(deviceStr, "1022") {
			inLaptop, err := pyrt.In("Laptop", platform)
			if err != nil {
				return false, err
			}
			if inLaptop {
				selected = append(selected, "GenericUSBXHCI")
			}
		} else {
			selected = append(selected, "XHCI-unsupported")
		}
	}

	sandy, err := pyrt.In("Sandy Bridge", codename)
	if err != nil {
		return false, err
	}
	if sandy {
		selected = append(selected, "ASPP-Override")
	}
	ivy, err := pyrt.In("Ivy Bridge", codename)
	if err != nil {
		return false, err
	}
	if sandy || ivy {
		selected = append(selected, "AppleIntelCPUPowerManagement", "AppleIntelCPUPowerManagementClient")
	}

	if needsOCLP {
		selected = append(selected, "AMFIPass", "RestrictEvents")
	}

	selected = uniqueStrings(selected)
	forced, err := m.VerifyKextCompatibility(selected, macosVersion)
	if err != nil {
		return false, err
	}
	for _, name := range selected {
		if _, err := m.CheckKext(lookup(name), macosVersion, forced[name], nil); err != nil {
			return false, err
		}
	}
	return needsOCLP, nil
}

// selectWifi mirrors the Wi-Fi part of the network loop for one interface. Each
// darwin comparison is evaluated only where the original's if/elif chain
// evaluates it, so an invalid macOS version fails in the same cases.
func (m *Maestro) selectWifi(selected []string, deviceID any, networkName string, macosVersion string, needsOCLP bool) ([]string, bool, error) {
	if pyrt.InStrings(deviceID, datasets.BroadcomWiFiIDs) {
		ok, err := darwinAtLeast(macosVersion, "23.0.0")
		if err != nil {
			return nil, false, err
		}
		if ok {
			selected = append(selected, "IOSkywalkFamily")
			ok25, err := darwinAtLeast(macosVersion, "25.0.0")
			if err != nil {
				return nil, false, err
			}
			if ok25 {
				selected = append(selected, "AirPortBrcmNIC-Tahoe")
			}
		}
	}

	broadcom := datasets.BroadcomWiFiIDs
	matched := false
	if pyrt.InStrings(deviceID, strSlice(broadcom, 0, 15)) {
		selected = append(selected, "AirportBrcmFixup")
		matched = true
	}
	if !matched && pyrt.Equal(deviceID, broadcom[15]) {
		ok, err := darwinAtLeast(macosVersion, "19.0.0")
		if err != nil {
			return nil, false, err
		}
		if ok {
			selected = append(selected, "AirportBrcmFixup")
			matched = true
		}
	}
	if !matched && pyrt.InStrings(deviceID, strSlice(broadcom, 16, 18)) {
		ok, err := darwinAtLeast(macosVersion, "20.0.0")
		if err != nil {
			return nil, false, err
		}
		if ok {
			selected = append(selected, "AirportBrcmFixup")
			matched = true
		}
	}
	if !matched && pyrt.InStrings(deviceID, datasets.IntelWiFiIDs) {
		choice, nOCLP, err := m.intelWifiChoice(macosVersion, needsOCLP)
		if err != nil {
			return nil, false, err
		}
		needsOCLP = nOCLP
		selected = append(selected, choice...)
		matched = true
	}
	if !matched && pyrt.InStrings(deviceID, strSlice(datasets.AtherosWiFiIDs, 0, 8)) {
		selected = append(selected, "corecaptureElCap")
		ok25, err := darwinAtLeast(macosVersion, "25.0.0")
		if err != nil {
			return nil, false, err
		}
		if ok25 {
			selected = append(selected, "AirPortAtheros40-Tahoe")
		}
		above, err := cmpVersion(macosVersion, "20.99.99")
		if err != nil {
			return nil, false, err
		}
		if above > 0 {
			selected = append(selected, "AMFIPass")
		}
		matched = true
	}
	if matched {
		return selected, needsOCLP, nil
	}

	single := []struct {
		ids  []string
		kext string
	}{
		{datasets.Rtw88WiFiIDs, "Feixiao"},
		{datasets.IntelI22XIDs, "AppleIGC"},
		{datasets.AtherosE2200IDs, "AtherosE2200Ethernet"},
		{datasets.IntelMausiIDs, "IntelMausiEthernet"},
		{datasets.RealtekRTL8100IDs, "RealtekRTL8100"},
		{datasets.RealtekRTL8111IDs, "RealtekRTL8111"},
		{datasets.AppleIGBIDs, "AppleIGB"},
		{datasets.BroadcomBCM57XXIDs, "CatalinaBCM5701Ethernet"},
		{datasets.IntelX500IDs, "IntelLucy"},
		{datasets.RealtekRTL812xIDs, "RTL812xLucy"},
	}
	for _, entry := range single {
		if pyrt.InStrings(deviceID, entry.ids) {
			selected = append(selected, entry.kext)
			break
		}
	}
	return selected, needsOCLP, nil
}

// intelWifiChoice mirrors the Intel Wi-Fi prompt: the laobamac fork is offered
// when its kext is compatible, and the answer picks the kext to install.
func (m *Maestro) intelWifiChoice(macosVersion string, needsOCLP bool) ([]string, bool, error) {
	darwin, err := macos.ParseVersion(macosVersion)
	if err != nil {
		return nil, false, err
	}
	laobamac, err := m.kextAt(lookup("AirportItlwm-laobamac"))
	if err != nil {
		return nil, false, err
	}
	nativeFork, err := m.IsKextCompatible(laobamac, macosVersion)
	if err != nil {
		return nil, false, err
	}
	options := []string{"AirportItlwm", "itlwm"}
	if nativeFork {
		options = append([]string{"AirportItlwm-laobamac"}, options...)
	}
	recommended := 2
	if nativeFork || darwin.Major < 23 {
		recommended = 1
	}

	macName, err := macos.MacOSName(macosVersion)
	if err != nil {
		return nil, false, err
	}
	var selectedName string
	if strings.Contains(macName, "Beta") && !nativeFork {
		selectedName = "itlwm"
	} else {
		answer, err := m.input(fmt.Sprintf("Select Intel Wi-Fi kext (default: %s): ", options[recommended-1]))
		if err != nil {
			return nil, false, err
		}
		choice := strings.TrimSpace(answer)
		if choice == "" {
			choice = strconv.Itoa(recommended)
		}
		n := 0
		if isDigits(choice) {
			n, _ = strconv.Atoi(choice)
		}
		if !isDigits(choice) || !(1 <= n && n <= len(options)) {
			choice = strconv.Itoa(recommended)
			n, _ = strconv.Atoi(choice)
		}
		selectedName = options[n-1]
	}

	out := []string{selectedName}
	if selectedName == "AirportItlwm" && darwin.Major >= 24 {
		needsOCLP = true
		out = append(out, "IOSkywalkFamily")
		if darwin.Major >= 25 {
			out = append(out, "AirPortBrcmNIC-Tahoe")
		}
	}
	return out, needsOCLP, nil
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
