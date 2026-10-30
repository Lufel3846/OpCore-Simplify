package config

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/datasets"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyrt"
)

// addDeviceProperty mirrors the nested add_device_property helper of
// deviceproperties: nothing is stored for an empty path or empty properties,
// and an existing entry is updated key by key.
func addDeviceProperty(add *plist.Dict, path any, properties *plist.Dict) error {
	if !pyrt.Truthy(path) || properties == nil || !pyrt.Truthy(properties) {
		return nil
	}
	key, ok := path.(string)
	if !ok {
		return fmt.Errorf("TypeError: PCI path %v is not a string", path)
	}
	existing, found := add.Get(key)
	if !found || !pyrt.Truthy(existing) {
		add.Set(key, plist.NewDict())
		existing, _ = add.Get(key)
	}
	entry := existing.(*plist.Dict)
	for _, k := range properties.Keys() {
		v, _ := properties.Get(k)
		entry.Set(k, v)
	}
	return nil
}

// props builds an ordered dictionary from alternating keys and values.
func props(kv ...any) *plist.Dict {
	d := plist.NewDict()
	for i := 0; i < len(kv); i += 2 {
		d.Set(kv[i].(string), kv[i+1])
	}
	return d
}

// deviceProperties mirrors Prodigy.deviceproperties. It returns the
// DeviceProperties > Add dictionary, keyed by PCI path.
func (p *Prodigy) deviceProperties(report any, disabled any, macosVersion string, kexts []*datasets.KextInfo) (*plist.Dict, error) {
	add := plist.NewDict()

	whatever, err := kextChecked(kexts, "WhateverGreen")
	if err != nil {
		return nil, err
	}
	if whatever {
		if err := p.gpuProperties(add, report, macosVersion); err != nil {
			return nil, err
		}
	}

	appleALC, err := kextChecked(kexts, "AppleALC")
	if err != nil {
		return nil, err
	}
	if appleALC {
		layout, controller, err := p.selectAudioCodecLayout(report, nil, true)
		if err != nil {
			return nil, err
		}
		if pyrt.Truthy(layout) && pyrt.Truthy(controller) {
			pci, err := pyrt.Get(controller, "PCI Path")
			if err != nil {
				return nil, err
			}
			if err := addDeviceProperty(add, pci, props("layout-id", layout)); err != nil {
				return nil, err
			}
		}
	}

	if err := networkProperties(add, report, macosVersion, kexts); err != nil {
		return nil, err
	}

	storageObj, err := pyrt.GetDefault(report, "Storage Controllers", plist.NewDict())
	if err != nil {
		return nil, err
	}
	storage, err := pyrt.Items(storageObj)
	if err != nil {
		return nil, err
	}
	for _, dev := range storage {
		acpi, err := pyrt.Get(dev.Value, "ACPI Path")
		if err != nil {
			return nil, err
		}
		if pyrt.Truthy(acpi) {
			continue
		}
		pci, err := pyrt.Get(dev.Value, "PCI Path")
		if err != nil {
			return nil, err
		}
		if err := addDeviceProperty(add, pci, props("built-in", "01")); err != nil {
			return nil, err
		}
	}

	disabledItems, err := pyrt.Items(disabled)
	if err != nil {
		return nil, err
	}
	for _, dev := range disabledItems {
		if !strings.Contains(dev.Key, "GPU") {
			continue
		}
		flag, err := pyrt.GetDefault(dev.Value, "Disabled", false)
		if err != nil {
			return nil, err
		}
		if pyrt.Truthy(flag) {
			continue
		}
		pci, err := pyrt.Get(dev.Value, "PCI Path")
		if err != nil {
			return nil, err
		}
		if err := addDeviceProperty(add, pci, props("disable-gpu", true)); err != nil {
			return nil, err
		}
	}

	// Strings are converted with hex_to_bytes; a non-hex string stays a string.
	for _, key := range add.Keys() {
		value, _ := add.Get(key)
		entry := value.(*plist.Dict)
		for _, childKey := range entry.Keys() {
			child, _ := entry.Get(childKey)
			if s, ok := child.(string); ok {
				entry.Set(childKey, hexToBytes(s))
			}
		}
	}
	return add, nil
}

// gpuProperties is the first block of deviceproperties, run when WhateverGreen
// is checked: integrated Intel GPUs, the Sandy/Ivy Bridge chipset IDs, and the
// discrete GPUs that need a device-id spoof.
func (p *Prodigy) gpuProperties(add *plist.Dict, report any, macosVersion string) error {
	const defaultGPUPath = "PciRoot(0x0)/Pci(0x2,0x0)"
	gpuObj, err := pyrt.GetDefault(report, "GPU", plist.NewDict())
	if err != nil {
		return err
	}
	gpus, err := pyrt.Items(gpuObj)
	if err != nil {
		return err
	}
	for _, gpu := range gpus {
		deviceType, err := pyrt.Get(gpu.Value, "Device Type")
		if err != nil {
			return err
		}
		switch {
		case pyrt.Equal(deviceType, "Integrated GPU"):
			if err := p.integratedGPU(add, report, gpu, macosVersion, defaultGPUPath); err != nil {
				return err
			}
		case pyrt.Equal(deviceType, "Discrete GPU"):
			if err := discreteGPU(add, gpu); err != nil {
				return err
			}
		}
	}
	return nil
}

// integratedGPU handles one "Integrated GPU" entry of the report.
func (p *Prodigy) integratedGPU(add *plist.Dict, report any, gpu pyrt.Pair, macosVersion, defaultPath string) error {
	mfr, err := pyrt.Get(gpu.Value, "Manufacturer")
	if err != nil {
		return err
	}
	intel, err := pyrt.In("Intel", mfr)
	if err != nil {
		return err
	}
	if !intel {
		return nil
	}

	pciRaw, err := pyrt.GetDefault(gpu.Value, "PCI Path", defaultPath)
	if err != nil {
		return err
	}
	board, err := pyrt.Get(report, "Motherboard")
	if err != nil {
		return err
	}
	boardName, err := pyrt.Get(board, "Name")
	if err != nil {
		return err
	}
	var platform any
	nuc, err := pyrt.In("NUC", boardName)
	if err != nil {
		return err
	}
	if nuc {
		platform = "NUC"
	} else {
		platform, err = pyrt.Get(board, "Platform")
		if err != nil {
			return err
		}
	}
	monitor, err := pyrt.GetDefault(report, "Monitor", plist.NewDict())
	if err != nil {
		return err
	}
	igpu, err := igpuProperties(platform, gpu.Key, gpu.Value, monitor, macosVersion)
	if err != nil {
		return err
	}
	if err := addDeviceProperty(add, pciRaw, igpu); err != nil {
		return err
	}

	// Python: if deviceproperties_add[PCI path]: — a missing key raises KeyError.
	key, ok := pciRaw.(string)
	if !ok {
		return errors.New("KeyError: PCI path is not a string")
	}
	entry, found := add.Get(key)
	if !found {
		return fmt.Errorf("KeyError: %q", key)
	}
	if !pyrt.Truthy(entry) {
		return nil
	}

	codename, err := pyrt.Get(gpu.Value, "Codename")
	if err != nil {
		return err
	}
	if !inList(codename, "Sandy Bridge", "Ivy Bridge") {
		return nil
	}
	return chipsetIDs(add, report, codename)
}

// chipsetIDs sets the device-id of the Sandy/Ivy Bridge HD graphics companions.
func chipsetIDs(add *plist.Dict, report any, codename any) error {
	sysObj, err := pyrt.GetDefault(report, "System Devices", plist.NewDict())
	if err != nil {
		return err
	}
	devices, err := pyrt.Items(sysObj)
	if err != nil {
		return err
	}
	for _, dev := range devices {
		devID, err := pyrt.Get(dev.Value, "Device ID")
		if err != nil {
			return err
		}
		if !inList(devID, "8086-1C3A", "8086-1E3A") {
			continue
		}
		devPCI, err := pyrt.GetDefault(dev.Value, "PCI Path", "PciRoot(0x16,0x0)")
		if err != nil {
			return err
		}
		id := devID.(string)
		sandy, err := pyrt.In("Sandy Bridge", codename)
		if err != nil {
			return err
		}
		if sandy && strings.Contains("8086-1E3A", id) {
			if err := addDeviceProperty(add, devPCI, props("device-id", "3A1C0000")); err != nil {
				return err
			}
			continue
		}
		ivy, err := pyrt.In("Ivy Bridge", codename)
		if err != nil {
			return err
		}
		if ivy && strings.Contains("8086-1C3A", id) {
			if err := addDeviceProperty(add, devPCI, props("device-id", "3A1E0000")); err != nil {
				return err
			}
		}
	}
	return nil
}

// discreteGPU spoofs the device-id of a discrete GPU listed in SpoofGPUIDs.
func discreteGPU(add *plist.Dict, gpu pyrt.Pair) error {
	gpuID, err := pyrt.Get(gpu.Value, "Device ID")
	if err != nil {
		return err
	}
	spoof, found := datasets.LookupID(datasets.SpoofGPUIDs, gpuID)
	if !found {
		return nil
	}
	if !spoof.IsString {
		return errors.New("AttributeError: spoof value is not a string")
	}
	parts := strings.Split(spoof.Text, "-")
	pci, err := pyrt.Get(gpu.Value, "PCI Path")
	if err != nil {
		return err
	}
	return addDeviceProperty(add, pci, props(
		"device-id", toLittleEndianHex(parts[len(parts)-1]),
		"model", gpu.Key,
	))
}

// networkProperties is the Network block of deviceproperties.
func networkProperties(add *plist.Dict, report any, macosVersion string, kexts []*datasets.KextInfo) error {
	netObj, err := pyrt.GetDefault(report, "Network", plist.NewDict())
	if err != nil {
		return err
	}
	networks, err := pyrt.Items(netObj)
	if err != nil {
		return err
	}
	for _, net := range networks {
		deviceID, err := pyrt.Get(net.Value, "Device ID")
		if err != nil {
			return err
		}
		pci, err := pyrt.Get(net.Value, "PCI Path")
		if err != nil {
			return err
		}
		if err := wifiAndEthernetMatch(add, deviceID, pci, macosVersion, kexts); err != nil {
			return err
		}
		acpi, err := pyrt.Get(net.Value, "ACPI Path")
		if err != nil {
			return err
		}
		if !pyrt.Truthy(acpi) {
			if err := addDeviceProperty(add, pci, props("built-in", "01")); err != nil {
				return err
			}
		}
	}
	return nil
}

// wifiAndEthernetMatch is the if/elif chain on the network device ID.
func wifiAndEthernetMatch(add *plist.Dict, deviceID, pci any, macosVersion string, kexts []*datasets.KextInfo) error {
	switch {
	case pyrt.InStrings(deviceID, datasets.AtherosWiFiIDs[6:8]):
		return addDeviceProperty(add, pci, props("IOName", "pci168c,2a", "device-id", hexToBytes("2A000000")))
	case pyrt.InStrings(deviceID, strSlice(datasets.BroadcomWiFiIDs, 0, 18)):
		return addDeviceProperty(add, pci, props("IOName", "pci14e4,43a0"))
	case pyrt.InStrings(deviceID, strSlice(datasets.BroadcomBCM57XXIDs, 0, len(datasets.BroadcomBCM57XXIDs)-5)):
		return addDeviceProperty(add, pci, props("IOName", "pci14e4,16b4", "device-id", hexToBytes("B4160000")))
	case pyrt.InStrings(deviceID, strSlice(datasets.AquantiaAqtionIDs, 0, 2)):
		return addDeviceProperty(add, pci, props("IOName", "1D6A-91B1"))
	case pyrt.InStrings(deviceID, datasets.IntelWiFiIDs):
		// all((AirportItlwm checked, darwin >= 24)) — both parts are evaluated.
		airport, err := kextChecked(kexts, "AirportItlwm")
		if err != nil {
			return err
		}
		atLeast24, err := darwinAtLeast(macosVersion, "24.0.0")
		if err != nil {
			return err
		}
		if !airport || !atLeast24 {
			return nil
		}
		skywalk, err := kextChecked(kexts, "IOSkywalkFamily")
		if err != nil {
			return err
		}
		legacy, err := kextChecked(kexts, "IO80211FamilyLegacy")
		if err != nil {
			return err
		}
		if skywalk && legacy {
			return addDeviceProperty(add, pci, props("IOName", "pci14e4,43a0"))
		}
	}
	return nil
}

// strSlice mirrors list[start:end] for a string list.
func strSlice(list []string, start, end int) []string {
	a, b := clampSlice(start, end, len(list))
	return list[a:b]
}
