package acpiguru

import (
	"fmt"
	"strings"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/datasets"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/dsdt"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyrt"
)

// bAddEntry builds one {"Comment", "Enabled", "Path"} entry of an "Add" list,
// writing the SSDT like the original.
func (g *Guru) bAddEntry(ssdtName, ssdtContent string) (*plist.Dict, error) {
	enabled, err := g.WriteSSDT(ssdtName, ssdtContent, true)
	if err != nil {
		return nil, err
	}
	e := plist.NewDict()
	e.Set("Comment", ssdtName+".aml")
	e.Set("Enabled", enabled)
	e.Set("Path", ssdtName+".aml")
	return e, nil
}

// bAddResult is {"Add": [entry]}.
func (g *Guru) bAddResult(ssdtName, ssdtContent string) (*plist.Dict, error) {
	e, err := g.bAddEntry(ssdtName, ssdtContent)
	if err != nil {
		return nil, err
	}
	d := plist.NewDict()
	d.Set("Add", []any{e})
	return d, nil
}

// bStaPatch converts a get_sta_var patch to the dict of the original.
func bStaPatch(p STAPatch) *plist.Dict {
	d := plist.NewDict()
	d.Set("Comment", p.Comment)
	d.Set("Find", p.Find)
	d.Set("Replace", p.Replace)
	return d
}

// bStaType is sta.get("sta_type", "MethodObj").
func bStaType(s *STAVar) string {
	if s.StaType == "" {
		return "MethodObj"
	}
	return s.StaType
}

// bContainsAny mirrors utils.contains_any(data, search_item) used as a truth value.
func bContainsAny(data []string, search string) bool {
	s := strings.ToLower(search)
	for _, item := range data {
		if strings.Contains(s, strings.ToLower(item)) {
			return true
		}
	}
	return false
}

// bPciBus is ".".join(self.lpc_bus_device.split(".")[:2]).
func (g *Guru) bPciBus() string {
	return strings.Join(pySliceStrings(strings.Split(g.LPCBusDevice, "."), 0, 2), ".")
}

func bLast(parts []string) string { return parts[len(parts)-1] }

// FakeEmbeddedController mirrors fake_embedded_controller.
func (g *Guru) FakeEmbeddedController() (*plist.Dict, error) {
	ssdtName := "SSDT-EC"
	platform, err := chain(g.HardwareReport, "Motherboard", "Platform")
	if err != nil {
		return nil, err
	}
	laptop, err := pyrt.In("Laptop", platform)
	if err != nil {
		return nil, err
	}

	var table *dsdt.Table
	staNeedsPatching := func(sta *STAVar) bool {
		if sta == nil || len(sta.Sta) == 0 {
			return false
		}
		switch sta.StaType {
		case "IntObj":
			idx := sta.Sta[0].Index
			if idx < -len(table.Lines) || idx >= len(table.Lines) {
				return true
			}
			if idx < 0 {
				idx += len(table.Lines)
			}
			if !strings.Contains(table.Lines[idx], "Name (_STA, 0x0F)") {
				return true
			}
		case "MethodObj":
			scope := strings.Join(g.Acpi.GetScope(sta.Sta[0].Index, false, true, table), "\n")
			if strings.Count(scope, "Return (") > 1 || !strings.Contains(scope, "Return (0x0F)") {
				return true
			}
		}
		return false
	}

	rename := false
	namedEC := false
	var ecToPatch, ecToEnable []string
	ecSta := map[string]*STAVar{}
	ecEnableSta := map[string]*STAVar{}
	patches := []any{}
	lpcName := ""
	hasLPC := false
	for _, tableName := range SortedNicely(g.Acpi.AcpiTables.Names()) {
		table, _ = g.Acpi.AcpiTables.Get(tableName)
		ecList := g.Acpi.GetDevicePathsWithHID("PNP0C09", table)
		if len(ecList) == 0 {
			continue
		}
		lpcName = strings.Join(pySliceStrings(strings.Split(ecList[0].Path, "."), 0, len(strings.Split(ecList[0].Path, "."))-1), ".")
		hasLPC = true
		for _, x := range ecList {
			device := x.Path
			origDevice := x.Path
			if bLast(strings.Split(device, ".")) == "EC" {
				namedEC = true
				if !laptop {
					parts := strings.Split(device, ".")
					device = strings.Join(append(append([]string{}, parts[:len(parts)-1]...), "EC0"), ".")
					rename = true
				}
			}
			scope := strings.Join(g.Acpi.GetScope(x.Index, false, true, table), "\n")
			if strings.Contains(scope, "_HID") && strings.Contains(scope, "_CRS") && strings.Contains(scope, "_GPE") {
				sta, err := g.GetStaVar("", origDevice, "PNP0C09", bLast(strings.Split(origDevice, ".")), false, table)
				if err != nil {
					return nil, err
				}
				if !laptop {
					ecToPatch = append(ecToPatch, device)
					if len(sta.Patches) > 0 {
						for _, p := range sta.Patches {
							patches = append(patches, bStaPatch(p))
						}
						ecSta[device] = sta
					}
				} else if len(sta.Patches) > 0 {
					if staNeedsPatching(sta) {
						ecToEnable = append(ecToEnable, device)
						ecEnableSta[device] = sta
						for _, p := range sta.Patches {
							d := bStaPatch(p)
							d.Set("Enabled", false)
							d.Set("Disabled", true)
							patches = append(patches, d)
						}
					}
				}
			}
		}
	}
	if laptop && namedEC && len(patches) == 0 {
		return nil, nil
	}
	if !hasLPC {
		lpcName, hasLPC = g.GetLPCName(false, true, true)
	}
	if !hasLPC {
		return nil, nil
	}
	if rename {
		extra := ""
		if len(ecSta) == 0 {
			extra = ""
		} else {
			extra = " - must come before any EC _STA to XSTA renames!"
		}
		d := plist.NewDict()
		d.Set("Comment", "EC to EC0"+extra)
		d.Set("Find", "45435f5f")
		d.Set("Replace", "4543305f")
		patches = append([]any{d}, patches...)
	}
	ssdt := strings.ReplaceAll(`
DefinitionBlock ("", "SSDT", 2, "ZPSS", "EC", 0x00001000)
{
    External ([[LPCName]], DeviceObj)
`, "[[LPCName]]", lpcName)
	for _, x := range ecToPatch {
		ssdt += fmt.Sprintf("    External (%s, DeviceObj)\n", x)
		if s, ok := ecSta[x]; ok {
			ssdt += fmt.Sprintf("    External (%s.XSTA, %s)\n", x, bStaType(s))
		}
	}
	for _, x := range ecToEnable {
		ssdt += fmt.Sprintf("    External (%s, DeviceObj)\n", x)
		if s, ok := ecEnableSta[x]; ok {
			t := bStaType(s)
			ssdt += fmt.Sprintf("    External (%s._STA, %s)\n    External (%s.XSTA, %s)\n", x, t, x, t)
		}
	}
	for _, x := range ecToPatch {
		xsta := "0x0F"
		if s, ok := ecSta[x]; ok {
			xsta = x + ".XSTA"
			if bStaType(s) == "MethodObj" {
				xsta += " ()"
			}
		}
		ssdt += strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(`
    Scope ([[ECName]])
    {
        Method (_STA, 0, NotSerialized)  // _STA: Status
        {
            If (_OSI ("Darwin"))
            {
                Return (0)
            }
            Else
            {
                Return ([[XSTA]])
            }
        }
    }
`, "[[LPCName]]", lpcName), "[[ECName]]", x), "[[XSTA]]", xsta)
	}
	for _, x := range ecToEnable {
		xsta := "Zero"
		if s, ok := ecEnableSta[x]; ok {
			xsta = x + ".XSTA"
			if bStaType(s) == "MethodObj" {
				xsta += " ()"
			}
		}
		ssdt += strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(`
    If (LAnd (CondRefOf ([[ECName]].XSTA), LNot (CondRefOf ([[ECName]]._STA))))
    {
        Scope ([[ECName]])
        {
            Method (_STA, 0, NotSerialized)  // _STA: Status
            {
                If (_OSI ("Darwin"))
                {
                    Return (0x0F)
                }
                Else
                {
                    Return ([[XSTA]])
                }
            }
        }
    }
`, "[[LPCName]]", lpcName), "[[ECName]]", x), "[[XSTA]]", xsta)
	}
	if !laptop || !namedEC {
		ssdt += strings.ReplaceAll(`
    Scope ([[LPCName]])
    {
        Device (EC)
        {
            Name (_HID, "ACID0001")  // _HID: Hardware ID
            Method (_STA, 0, NotSerialized)  // _STA: Status
            {
                If (_OSI ("Darwin"))
                {
                    Return (0x0F)
                }
                Else
                {
                    Return (Zero)
                }
            }
        }
    }`, "[[LPCName]]", lpcName)
	}
	ssdt += `
}`

	entry, err := g.bAddEntry(ssdtName, ssdt)
	if err != nil {
		return nil, err
	}
	d := plist.NewDict()
	d.Set("Add", []any{entry})
	d.Set("Patch", patches)
	return d, nil
}

// AddIntelManagementEngine mirrors add_intel_management_engine.
func (g *Guru) AddIntelManagementEngine() (*plist.Dict, error) {
	ssdtName := "SSDT-IMEI"
	ssdtContent := `
DefinitionBlock ("", "SSDT", 2, "ZPSS", "IMEI", 0x00000000)
{
    External (_SB_.PCI0, DeviceObj)

    Scope (_SB.PCI0)
    {
        Device (IMEI)
        {
            Name (_ADR, 0x00160000)  // _ADR: Address
            Method (_STA, 0, NotSerialized)  // _STA: Status
            {
                If (_OSI ("Darwin"))
                {
                    Return (0x0F)
                }
                Else
                {
                    Return (Zero)
                }
            }
        }
    }
}`
	if len(g.Acpi.GetDevicePathsWithHID("0x00160000", g.Dsdt)) == 0 {
		return g.bAddResult(ssdtName, ssdtContent)
	}
	return nil, nil
}

// AddMemoryControllerDevice mirrors add_memory_controller_device.
func (g *Guru) AddMemoryControllerDevice() (*plist.Dict, error) {
	if g.LPCBusDevice == "" {
		return nil, nil
	}
	ssdtName := "SSDT-MCHC"
	ssdtContent := `
DefinitionBlock ("", "SSDT", 2, "ZPSS", "MCHC", 0)
{
    External ([[PCIName]], DeviceObj)

    Scope ([[PCIName]])
    {
        Device (MCHC)
        {
            Name (_ADR, Zero)
            Method (_STA, 0, NotSerialized)
            {
                If (_OSI ("Darwin"))
                {
                    Return (0x0F)
                }
                Else
                {
                    Return (Zero)
                }
            }
        }
    }
}`
	if len(g.Acpi.GetDevicePaths("MCHC", g.Dsdt)) > 0 {
		return nil, nil
	}
	ssdtContent = strings.ReplaceAll(ssdtContent, "[[PCIName]]", g.bPciBus())
	return g.bAddResult(ssdtName, ssdtContent)
}

// bSmbusDeviceName is the try/except of add_system_management_bus_device.
func (g *Guru) bSmbusDeviceName() string {
	codename, err := chain(g.HardwareReport, "CPU", "Codename")
	if err != nil {
		return "SBUS"
	}
	hid := "0x001F0004"
	if pyrt.InStrings(codename, pySliceStrings(datasets.IntelCPUGenerations, 50, len(datasets.IntelCPUGenerations))) {
		hid = "0x001F0003"
	}
	devs := g.Acpi.GetDevicePathsWithHID(hid, g.Dsdt)
	if len(devs) == 0 {
		return "SBUS"
	}
	return bLast(strings.Split(devs[0].Path, "."))
}

// AddSystemManagementBusDevice mirrors add_system_management_bus_device.
func (g *Guru) AddSystemManagementBusDevice() (*plist.Dict, error) {
	if g.LPCBusDevice == "" {
		return nil, nil
	}
	smbusDeviceName := g.bSmbusDeviceName()
	smbusDevicePath := g.bPciBus() + "." + smbusDeviceName
	ssdtName := "SSDT-" + smbusDeviceName
	ssdtContent := strings.ReplaceAll(strings.ReplaceAll(`
DefinitionBlock ("", "SSDT", 2, "ZPSS", "[[SMBUSName]]", 0)
{
    External ([[SMBUSDevice]], DeviceObj)

    Scope ([[SMBUSDevice]])
    {
        Device (BUS0)
        {
            Name (_CID, "smbus")
            Name (_ADR, Zero)
            Method (_STA, 0, NotSerialized)
            {
                If (_OSI ("Darwin"))
                {
                    Return (0x0F)
                }
                Else
                {
                    Return (Zero)
                }
            }
        }
    }
}`, "[[SMBUSName]]", smbusDeviceName), "[[SMBUSDevice]]", smbusDevicePath)
	return g.bAddResult(ssdtName, ssdtContent)
}

// AddUSBPowerProperties mirrors add_usb_power_properties.
func (g *Guru) AddUSBPowerProperties() (*plist.Dict, error) {
	ssdtName := "SSDT-USBX"
	ssdtContent := `
DefinitionBlock ("", "SSDT", 2, "ZPSS", "USBX", 0x00001000)
{
    Scope (\_SB)
    {
        Device (USBX)
        {
            Name (_ADR, Zero)  // _ADR: Address
            Method (_DSM, 4, NotSerialized)  // _DSM: Device-Specific Method
            {
                If (LNot (Arg2))
                {
                    Return (Buffer ()
                    {
                        0x03
                    })
                }
                Return (Package ()
                {[[USBX_PROPS]]
                })
            }
            Method (_STA, 0, NotSerialized)  // _STA: Status
            {
                If (_OSI ("Darwin"))
                {
                    Return (0x0F)
                }
                Else
                {
                    Return (Zero)
                }
            }
        }
    }
}`

	type kv struct{ k, v string }
	var props []kv
	switch {
	case bContainsAny([]string{"MacPro7,1", "iMacPro1,1", "iMac20,", "iMac19,", "iMac18,", "iMac17,", "iMac16,"}, g.SMBIOSModel):
		props = []kv{{"kUSBSleepPowerSupply", "0x13EC"}, {"kUSBSleepPortCurrentLimit", "0x0834"}, {"kUSBWakePowerSupply", "0x13EC"}, {"kUSBWakePortCurrentLimit", "0x0834"}}
	case strings.Contains(g.SMBIOSModel, "MacMini8,1"):
		props = []kv{{"kUSBSleepPowerSupply", "0x0C80"}, {"kUSBSleepPortCurrentLimit", "0x0834"}, {"kUSBWakePowerSupply", "0x0C80"}, {"kUSBWakePortCurrentLimit", "0x0834"}}
	case bContainsAny([]string{"MacBookPro16,", "MacBookPro15,", "MacBookPro14,", "MacBookPro13,", "MacBookAir9,1"}, g.SMBIOSModel):
		props = []kv{{"kUSBSleepPortCurrentLimit", "0x0BB8"}, {"kUSBWakePortCurrentLimit", "0x0BB8"}}
	case strings.Contains(g.SMBIOSModel, "MacBook9,1"):
		props = []kv{{"kUSBSleepPowerSupply", "0x05DC"}, {"kUSBSleepPortCurrentLimit", "0x05DC"}, {"kUSBWakePowerSupply", "0x05DC"}, {"kUSBWakePortCurrentLimit", "0x05DC"}}
	}
	if len(props) == 0 {
		return nil, nil
	}
	parts := make([]string, len(props))
	for i, p := range props {
		parts[i] = "\n                    \"" + p.k + "\",\n                    " + p.v
	}
	ssdtContent = strings.ReplaceAll(ssdtContent, "[[USBX_PROPS]]", strings.Join(parts, ","))
	return g.bAddResult(ssdtName, ssdtContent)
}

// AmbientLightSensor mirrors ambient_light_sensor.
func (g *Guru) AmbientLightSensor() (*plist.Dict, error) {
	ssdtName := "SSDT-ALS0"
	ssdtContent := `
// Resource: https://github.com/acidanthera/OpenCorePkg/blob/master/Docs/AcpiSamples/Source/SSDT-ALS0.dsl

/*
 * Starting with macOS 10.15 Ambient Light Sensor presence is required for backlight functioning.
 * Here we create an Ambient Light Sensor ACPI Device, which can be used by SMCLightSensor kext
 * to report either dummy (when no device is present) or valid values through SMC interface.
 */
DefinitionBlock ("", "SSDT", 2, "ZPSS", "ALS0", 0x00000000)
{
    Scope (_SB)
    {
        Device (ALS0)
        {
            Name (_HID, "ACPI0008" /* Ambient Light Sensor Device */)  // _HID: Hardware ID
            Name (_CID, "smc-als")  // _CID: Compatible ID
            Name (_ALI, 0x012C)  // _ALI: Ambient Light Illuminance
            Name (_ALR, Package (0x01)  // _ALR: Ambient Light Response
            {
                Package (0x02)
                {
                    0x64, 
                    0x012C
                }
            })
            Method (_STA, 0, NotSerialized)  // _STA: Status
            {
                If (_OSI ("Darwin"))
                {
                    Return (0x0F)
                }
                Else
                {
                    Return (Zero)
                }
            }
        }
    }
}`
	alsDevice := ""
	if devs := g.Acpi.GetDevicePathsWithHID("ACPI0008", g.Dsdt); len(devs) > 0 {
		alsDevice = devs[0].Path
	}
	patches := []any{}
	if alsDevice != "" {
		alsDeviceName := bLast(strings.Split(alsDevice, "."))
		if !strings.Contains(alsDevice, ".") {
			alsDeviceName = pyStrSliceFrom(alsDeviceName, 1)
		}
		sta, err := g.GetStaVar("", "", "ACPI0008", alsDeviceName, false, g.Dsdt)
		if err != nil {
			return nil, err
		}
		for _, p := range sta.Patches {
			patches = append(patches, bStaPatch(p))
		}
		ssdtName = "SSDT-" + alsDeviceName
		xsta := alsDevice + ".XSTA"
		if bStaType(sta) == "MethodObj" {
			xsta += " ()"
		}
		ssdtContent = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(`
DefinitionBlock ("", "SSDT", 2, "ZPSS", "[[ALSName]]", 0x00000000)
{
    External ([[ALSDevice]], DeviceObj)
    External ([[ALSDevice]].XSTA, [[STAType]])

    Scope ([[ALSDevice]])
    {
        Method (_STA, 0, NotSerialized)  // _STA: Status
        {
            If (_OSI ("Darwin"))
            {
                Return (0x0F)
            }
            Else
            {
                Return ([[XSTA]])
            }
        }
    }
}`,
			"[[ALSName]]", alsDeviceName),
			"[[ALSDevice]]", alsDevice),
			"[[STAType]]", bStaType(sta)),
			"[[XSTA]]", xsta)
	}
	entry, err := g.bAddEntry(ssdtName, ssdtContent)
	if err != nil {
		return nil, err
	}
	d := plist.NewDict()
	d.Set("Add", []any{entry})
	d.Set("Patch", patches)
	return d, nil
}

// pyStrSliceFrom is s[n:] for a non-negative n.
func pyStrSliceFrom(s string, n int) string {
	if n >= len(s) {
		return ""
	}
	return s[n:]
}

// FindallPowerResourceBlocks mirrors findall_power_resource_blocks: (start, end) pairs.
func (g *Guru) FindallPowerResourceBlocks(tableLines []string) [][2]int {
	var blocks [][2]int
	i := 0
	for i < len(tableLines) {
		line := pyStrip(tableLines[i])
		if strings.HasPrefix(line, "PowerResource") {
			start := i
			open := 1
			i++
			for i < len(tableLines) && open > 0 {
				if strings.Contains(tableLines[i], "{") {
					open += strings.Count(tableLines[i], "{")
				}
				if strings.Contains(tableLines[i], "}") {
					open -= strings.Count(tableLines[i], "}")
				}
				i++
			}
			blocks = append(blocks, [2]int{start, i - 1})
		} else {
			i++
		}
	}
	return blocks
}

// IsMethodInPowerResource mirrors is_method_in_power_resource.
func (g *Guru) IsMethodInPowerResource(method dsdt.Path, tableLines []string) bool {
	for _, b := range g.FindallPowerResourceBlocks(tableLines) {
		if b[0] <= method.Index && method.Index <= b[1] {
			return true
		}
	}
	return false
}

// DisableUnsupportedDevice mirrors disable_unsupported_device.
func (g *Guru) DisableUnsupportedDevice() (*plist.Dict, error) {
	add := []any{}
	items, err := pyrt.Items(g.DisabledDevices)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		deviceName, props := item.Key, item.Value
		bus, err := pyrt.GetDefault(props, "Bus Type", "PCI")
		if err != nil {
			return nil, err
		}
		acpiPath, err := pyrt.Get(props, "ACPI Path")
		if err != nil {
			return nil, err
		}
		if !pyrt.Equal(bus, "PCI") || !pyrt.Truthy(acpiPath) {
			continue
		}
		path, pathErr := pyrt.Str(acpiPath)
		pathParts := strings.Split(path, ".")
		pathAt := func(i int) (string, error) {
			if pathErr != nil {
				return "", pathErr
			}
			if i < 0 {
				i += len(pathParts)
			}
			if i < 0 || i >= len(pathParts) {
				return "", fmt.Errorf("IndexError: list index out of range")
			}
			return pathParts[i], nil
		}

		ssdtName := ""
		ssdtContent := ""
		devType, err := pyrt.Get(props, "Device Type")
		if err != nil {
			return nil, err
		}
		busGet, err := pyrt.Get(props, "Bus Type")
		if err != nil {
			return nil, err
		}
		switch {
		case strings.Contains(deviceName, "GPU") && !pyrt.Equal(devType, "Integrated GPU"):
			p, err := pathAt(2)
			if err != nil {
				return nil, err
			}
			ssdtName = "SSDT-Disable_GPU_" + p
			targetDevice := path

			offFound, ps3Found := false, false
			for _, tableName := range g.Acpi.AcpiTables.Names() {
				tableData, _ := g.Acpi.AcpiTables.Get(tableName)
				offMethods := g.Acpi.GetMethodPaths("_OFF", tableData)
				ps3Methods := g.Acpi.GetMethodPaths("_PS3", tableData)
				if !offFound {
					for _, m := range offMethods {
						if strings.HasPrefix(m.Path, targetDevice) && !g.IsMethodInPowerResource(m, tableData.Lines) {
							offFound = true
							break
						}
					}
				}
				if !ps3Found {
					for _, m := range ps3Methods {
						if strings.HasPrefix(m.Path, targetDevice) {
							ps3Found = true
							break
						}
					}
				}
			}
			if !offFound && !ps3Found {
				continue
			}
			if offFound {
				ps3Found = false
			}
			dp, ok := props.(*plist.Dict)
			if !ok {
				return nil, fmt.Errorf("%w: object does not support item assignment", pyrt.ErrType)
			}
			dp.Set("Disabled", true)

			ssdtContent = `
DefinitionBlock ("", "SSDT", 2, "ZPSS", "DGPU", 0x00000000)
{`
			if offFound {
				ssdtContent += `
    External ([[DevicePath]]._OFF, MethodObj)
    External ([[DevicePath]]._ON_, MethodObj)`
			}
			if ps3Found {
				ssdtContent += `
    External ([[DevicePath]]._PS0, MethodObj)
    External ([[DevicePath]]._PS3, MethodObj)
    External ([[DevicePath]]._DSM, MethodObj)
`
			}
			ssdtContent += `
    Device (DGPU)
    {
        Name (_HID, "DGPU1000")
        Method (_INI, 0, NotSerialized)
        {
            _OFF ()
        }

        Method (_STA, 0, NotSerialized)
        {
            If (_OSI ("Darwin"))
            {
                Return (0x0F)
            }
            Else
            {
                Return (Zero)
            }
        }

        Method (_ON, 0, NotSerialized)
        {
`
			if offFound {
				ssdtContent += `
            [[DevicePath]]._ON ()
            `
			}
			if ps3Found {
				ssdtContent += `
            [[DevicePath]]._PS0 ()
            `
			}
			ssdtContent += `
        }

        Method (_OFF, 0, NotSerialized)
        {
`
			if offFound {
				ssdtContent += `
            [[DevicePath]]._OFF ()
            `
			}
			if ps3Found {
				ssdtContent += `
            [[DevicePath]]._DSM (ToUUID ("a486d8f8-0bda-471b-a72b-6042a6b5bee0") /* Unknown UUID */, 0x0100, 0x1A, Buffer (0x04)
            {
                    0x01, 0x00, 0x00, 0x03                           // ....
            })
            [[DevicePath]]._PS3 ()
            `
			}
			ssdtContent += "\n        }\n    }\n}"
		case strings.Contains(deviceName, "Network") && pyrt.Equal(busGet, "PCI"):
			p, err := pathAt(2)
			if err != nil {
				return nil, err
			}
			ssdtName = "SSDT-Disable_Network_" + p
			ssdtContent = `
DefinitionBlock ("", "SSDT", 2, "ZPSS", "DNET", 0x00000000)
{
    External ([[DevicePath]], DeviceObj)

    Method ([[DevicePath]]._DSM, 4, NotSerialized)  // _DSM: Device-Specific Method
    {
        If ((!Arg2 || (_OSI ("Darwin") == Zero)))
        {
            Return (Buffer (One)
            {
                 0x03                                             // .
            })
        }

        Return (Package (0x0A)
        {
            "name", 
            Buffer (0x09)
            {
                "#network"
            }, 

            "IOName", 
            "#display", 
            "class-code", 
            Buffer (0x04)
            {
                 0xFF, 0xFF, 0xFF, 0xFF                           // ....
            }, 

            "vendor-id", 
            Buffer (0x04)
            {
                 0xFF, 0xFF, 0x00, 0x00                           // ....
            }, 

            "device-id", 
            Buffer (0x04)
            {
                 0xFF, 0xFF, 0x00, 0x00                           // ....
            }
        })
    }
}
`
		case strings.Contains(deviceName, "Storage"):
			p, err := pathAt(-2)
			if err != nil {
				return nil, err
			}
			ssdtName = "SSDT-Disable_NVMe_" + p
			ssdtContent = `
DefinitionBlock ("", "SSDT", 2, "ZPSS", "DNVMe", 0x00000000)
{
    External ([[DevicePath]], DeviceObj)

    Method ([[DevicePath]]._DSM, 4, NotSerialized)  // _DSM: Device-Specific Method
    {
        If (_OSI ("Darwin"))
        {
            If (!Arg2)
            {
                Return (Buffer (One)
                {
                     0x03                                             // .
                })
            }

            Return (Package (0x02)
            {
                "class-code", 
                Buffer (0x04)
                {
                     0xFF, 0x08, 0x01, 0x00                           // ....
                }
            })
        }
    }
}
`
		}

		if ssdtName != "" {
			if pathErr != nil {
				return nil, pathErr
			}
			ssdtContent = strings.ReplaceAll(ssdtContent, "[[DevicePath]]", path)
			e, err := g.bAddEntry(ssdtName, ssdtContent)
			if err != nil {
				return nil, err
			}
			add = append(add, e)
		}
	}
	d := plist.NewDict()
	d.Set("Add", add)
	return d, nil
}
