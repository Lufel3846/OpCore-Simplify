package acpiguru

import (
	"bytes"
	"fmt"
	"math/rand"
	"strconv"
	"strings"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyrt"
)

// Ported from Scripts/acpi_guru.py (group C). The cT_* constants are the SSDT
// text templates, extracted from the Python source byte for byte.

// cT_enable_backlight_controls_1: Python line 2074
const cT_enable_backlight_controls_1 = `
DefinitionBlock ("", "SSDT", 2, "ZPSS", "PNLF", 0x00000000)
{`

// cT_enable_backlight_controls_2: Python line 2078
const cT_enable_backlight_controls_2 = `
    External ([[DevicePath]], DeviceObj)
    Device ([[DevicePath]].PNLF)`

// cT_enable_backlight_controls_3: Python line 2080
const cT_enable_backlight_controls_3 = `
    Device (PNLF)`

// cT_enable_backlight_controls_4: Python line 2081
const cT_enable_backlight_controls_4 = `
    {
        Name (_HID, EisaId ("APP0002"))  // _HID: Hardware ID
        Name (_CID, "backlight")  // _CID: Compatible ID
        Name (_UID, [[uid_value]])  // _UID: Unique ID
        
        Method (_STA, 0, NotSerialized)  // _STA: Status
        {
            If (_OSI ("Darwin"))
            {
                Return (0x0B)
            }
            Else
            {
                Return (Zero)
            }
        }`

// cT_enable_backlight_controls_5: Python line 2099
const cT_enable_backlight_controls_5 = `
        Method (_INI, 0, Serialized)
        {
            If (_OSI ("Darwin"))
            {
                OperationRegion ([[DevicePath]].RMP3, PCI_Config, Zero, 0x14)
                Field ([[DevicePath]].RMP3, AnyAcc, NoLock, Preserve)
                {
                    Offset (0x02), GDID,16,
                    Offset (0x10), BAR1,32,
                }
                // IGPU PWM backlight register descriptions:
                //   LEV2 not currently used
                //   LEVL level of backlight in Sandy/Ivy
                //   P0BL counter, when zero is vertical blank
                //   GRAN see description below in INI1 method
                //   LEVW should be initialized to 0xC0000000
                //   LEVX PWMMax except FBTYPE_HSWPLUS combo of max/level (Sandy/Ivy stored in MSW)
                //   LEVD level of backlight for Coffeelake
                //   PCHL not currently used
                OperationRegion (RMB1, SystemMemory, BAR1 & ~0xF, 0xe1184)
                Field(RMB1, AnyAcc, Lock, Preserve)
                {
                    Offset (0x48250),
                    LEV2, 32,
                    LEVL, 32,
                    Offset (0x70040),
                    P0BL, 32,
                    Offset (0xc2000),
                    GRAN, 32,
                    Offset (0xc8250),
                    LEVW, 32,
                    LEVX, 32,
                    LEVD, 32,
                    Offset (0xe1180),
                    PCHL, 32,
                }
                // Now fixup the backlight PWM depending on the framebuffer type
                // At this point:
                //   Local4 is RMCF.BLKT value (unused here), if specified (default is 1)
                //   Local0 is device-id for IGPU
                //   Local2 is LMAX, if specified (Ones means based on device-id)
                //   Local3 is framebuffer type

                // Adjustment required when using WhateverGreen.kext
                Local0 = GDID
                Local2 = Ones
                Local3 = 0

                // check Sandy/Ivy
                // #define FBTYPE_SANDYIVY 1
                If (LOr (LEqual (1, Local3), LNotEqual (Match (Package()
                {
                    // Sandy HD3000
                    0x010b, 0x0102,
                    0x0106, 0x1106, 0x1601, 0x0116, 0x0126,
                    0x0112, 0x0122,
                    // Ivy
                    0x0152, 0x0156, 0x0162, 0x0166,
                    0x016a,
                    // Arrandale
                    0x0046, 0x0042,
                }, MEQ, Local0, MTR, 0, 0), Ones)))
                {
                    if (LEqual (Local2, Ones))
                    {
                        // #define SANDYIVY_PWMMAX 0x710
                        Store (0x710, Local2)
                    }
                    // change/scale only if different than current...
                    Store (LEVX >> 16, Local1)
                    If (LNot (Local1))
                    {
                        Store (Local2, Local1)
                    }
                    If (LNotEqual (Local2, Local1))
                    {
                        // set new backlight PWMMax but retain current backlight level by scaling
                        Store ((LEVL * Local2) / Local1, Local0)
                        Store (Local2 << 16, Local3)
                        If (LGreater (Local2, Local1))
                        {
                            // PWMMax is getting larger... store new PWMMax first
                            Store (Local3, LEVX)
                            Store (Local0, LEVL)
                        }
                        Else
                        {
                            // otherwise, store new brightness level, followed by new PWMMax
                            Store (Local0, LEVL)
                            Store (Local3, LEVX)
                        }
                    }
                }
            }
        }`

// cT_enable_backlight_controls_6: Python line 2195
const cT_enable_backlight_controls_6 = `
    }
}`

// cT_enable_gpio_device_1: Python line 2223
const cT_enable_gpio_device_1 = `
DefinitionBlock ("", "SSDT", 2, "ZPSS", "GPI0", 0x00000000)
{
    External ([[GPI0Path]], DeviceObj)
    External ([[GPI0Path]].XSTA, [[STAType]])

    Scope ([[GPI0Path]])
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
}`

// cT_enable_nvram_support_1: Python line 2263
const cT_enable_nvram_support_1 = `
// Resource: https://github.com/acidanthera/OpenCorePkg/blob/master/Docs/AcpiSamples/Source/SSDT-PMC.dsl

/*
 * Intel 300-series PMC support for macOS
 *
 * Starting from Z390 chipsets PMC (D31:F2) is only available through MMIO.
 * Since there is no standard device for PMC in ACPI, Apple introduced its
 * own naming "APP9876" to access this device from AppleIntelPCHPMC driver.
 * To avoid confusion we disable this device for all other operating systems,
 * as they normally use another non-standard device with "PNP0C02" HID and
 * "PCHRESV" UID.
 *
 * On certain implementations, including APTIO V, PMC initialisation is
 * required for NVRAM access. Otherwise it will freeze in SMM mode.
 * The reason for this is rather unclear. Note, that PMC and SPI are
 * located in separate memory regions and PCHRESV maps both, yet only
 * PMC region is used by AppleIntelPCHPMC:
 * 0xFE000000~0xFE00FFFF - PMC MBAR
 * 0xFE010000~0xFE010FFF - SPI BAR0
 * 0xFE020000~0xFE035FFF - SerialIo BAR in ACPI mode
 *
 * PMC device has nothing to do to LPC bus, but is added to its scope for
 * faster initialisation. If we add it to PCI0, where it normally exists,
 * it will start in the end of PCI configuration, which is too late for
 * NVRAM support.
 */
DefinitionBlock ("", "SSDT", 2, "ACDT", "PMCR", 0x00001000)
{
    External ([[LPCPath]], DeviceObj)

    Scope ([[LPCPath]])
    {
        Device (PMCR)
        {
            Name (_HID, EisaId ("APP9876"))  // _HID: Hardware ID
            Method (_STA, 0, NotSerialized)  // _STA: Status
            {
                If (_OSI ("Darwin"))
                {
                    Return (0x0B)
                }
                Else
                {
                    Return (Zero)
                }
            }
            Name (_CRS, ResourceTemplate ()  // _CRS: Current Resource Settings
            {
                Memory32Fixed (ReadWrite,
                    0xFE000000,         // Address Base
                    0x00010000,         // Address Length
                    )
            })
        }
    }
}`

// cT_add_null_ethernet_device_1: Python line 2362
const cT_add_null_ethernet_device_1 = `
// Resource: https://github.com/RehabMan/OS-X-Null-Ethernet/blob/master/SSDT-RMNE.dsl

/* ssdt.dsl -- SSDT injector for NullEthernet
 *
 * Copyright (c) 2014 RehabMan <racerrehabman@gmail.com>
 * All rights reserved.
 *
 * This program is free software; you can redistribute it and/or modify it
 * under the terms of the GNU General Public License as published by the Free
 * Software Foundation; either version 2 of the License, or (at your option)
 * any later version.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.  See the GNU General Public License for
 * more details.
 *
 */

// Use this SSDT as an alternative to patching your DSDT...

DefinitionBlock("", "SSDT", 2, "ZPSS", "RMNE", 0x00001000)
{
    Device (RMNE)
    {
        Name (_ADR, Zero)
        // The NullEthernet kext matches on this HID
        Name (_HID, "NULE0000")
        // This is the MAC address returned by the kext. Modify if necessary.
        Name (MAC, Buffer() { [[MACAddress]] })
        Method (_DSM, 4, NotSerialized)
        {
            If (LEqual (Arg2, Zero)) { Return (Buffer() { 0x03 } ) }
            Return (Package()
            {
                "built-in", Buffer() { 0x00 },
                "IOName", "ethernet",
                "name", Buffer() { "ethernet" },
                "model", Buffer() { "RM-NullEthernet-1001" },
                "device_type", Buffer() { "ethernet" },
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
}`

// cT_fix_system_clock_hedt_1: Python line 2453
const cT_fix_system_clock_hedt_1 = `
DefinitionBlock ("", "SSDT", 2, "ZPSS", "RtcRange", 0x00000000)
{`

// cT_fix_system_clock_hedt_2: Python line 2460
const cT_fix_system_clock_hedt_2 = `
    External ([[device_path]], DeviceObj)
    
    Scope ([[device_path]])
    {
        Method (_STA, 0, NotSerialized)  // _STA: Status
        {
            If (_OSI ("Darwin"))
            {
                Return (Zero)
            }
            Else
            {
                Return (0x0F)
            }
        }
    }`

// cT_fix_system_clock_hedt_3: Python line 2477
const cT_fix_system_clock_hedt_3 = `
    External ([[parent_path]], DeviceObj)

    Device ([[device_path]])
    {
        Name (_HID, EisaId ("PNP0B00") /* AT Real-Time Clock */)  // _HID: Hardware ID
        Name (_CRS, ResourceTemplate ()  // _CRS: Current Resource Settings
        {
            IO (Decode16,
                0x0070,             // Range Minimum
                0x0070,             // Range Maximum
                0x01,               // Alignment
                0x04,               // Length
                )
            IO (Decode16,
                0x0074,             // Range Minimum
                0x0074,             // Range Maximum
                0x01,               // Alignment
                0x04,               // Length
                )
            IRQNoFlags ()
                {8}
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
}`

// cT_instant_wake_fix_1: Python line 2557
const cT_instant_wake_fix_1 = `
// Resource: https://github.com/5T33Z0/OC-Little-Translated/blob/main/04_Fixing_Sleep_and_Wake_Issues/060D_Instant_Wake_Fix/README.md

DefinitionBlock ("", "SSDT", 2, "ZPSS", "_PRW", 0x00000000)
{`

// cT_instant_wake_fix_2: Python line 2563
const cT_instant_wake_fix_2 = `
    External(XPRW, MethodObj)`

// cT_instant_wake_fix_3: Python line 2565
const cT_instant_wake_fix_3 = `
    External (USWE, FieldUnitObj)`

// cT_instant_wake_fix_4: Python line 2567
const cT_instant_wake_fix_4 = `
    External (WOLE, FieldUnitObj)`

// cT_instant_wake_fix_5: Python line 2569
const cT_instant_wake_fix_5 = `

    Scope (\)
    {
        If (_OSI ("Darwin"))
        {`

// cT_instant_wake_fix_6: Python line 2575
const cT_instant_wake_fix_6 = `
            USWE = Zero`

// cT_instant_wake_fix_7: Python line 2577
const cT_instant_wake_fix_7 = `
            WOLE = Zero`

// cT_instant_wake_fix_8: Python line 2578
const cT_instant_wake_fix_8 = `        }
    }`

// cT_instant_wake_fix_9: Python line 2581
const cT_instant_wake_fix_9 = `
    Method (GPRW, 2, NotSerialized)
    {
        If (_OSI ("Darwin"))
        {
            If ((0x6D == Arg0))
            {
                Return (Package ()
                {
                    0x6D, 
                    Zero
                })
            }

            If ((0x0D == Arg0))
            {
                Return (Package ()
                {
                    0x0D, 
                    Zero
                })
            }
        }
        Return (XPRW (Arg0, Arg1))
    }`

// cT_instant_wake_fix_10: Python line 2607
const cT_instant_wake_fix_10 = `
    Method (UPRW, 2, NotSerialized)
    {
        If (_OSI ("Darwin"))
        {
            If ((0x6D == Arg0))
            {
                Return (Package ()
                {
                    0x6D, 
                    Zero
                })
            }

            If ((0x0D == Arg0))
            {
                Return (Package ()
                {
                    0x0D, 
                    Zero
                })
            }
        }
        Return (XPRW (Arg0, Arg1))
    }`

// cT_instant_wake_fix_11: Python line 2632
const cT_instant_wake_fix_11 = `
}`

// cT_fix_uncore_bridge_1: Python line 2653
const cT_fix_uncore_bridge_1 = `
// Resource: https://github.com/acidanthera/OpenCorePkg/blob/master/Docs/AcpiSamples/Source/SSDT-UNC.dsl

/*
 * Discovered on X99-series.
 * These platforms have uncore PCI bridges for 4 CPU sockets
 * present in ACPI despite having none physically.
 *
 * Under normal conditions these are disabled depending on
 * CPU presence in the socket via Processor Bit Mask (PRBM),
 * but on X99 this code is unused or broken as such bridges
 * simply do not exist. We fix that by writing 0 to PRBM.
 *
 * Doing so is important as starting with macOS 11 IOPCIFamily
 * will crash as soon as it sees non-existent PCI bridges.
 */

DefinitionBlock ("", "SSDT", 2, "ZPSS", "UNC", 0x00000000)
{
    External (_SB.UNC0, DeviceObj)
    External (PRBM, IntObj)

    Scope (_SB.UNC0)
    {
        Method (_INI, 0, NotSerialized)
        {
            // In most cases this patch does benefit all operating systems,
            // yet on select pre-Windows 10 it may cause issues.
            // Remove If (_OSI ("Darwin")) in case you have none.
            If (_OSI ("Darwin")) {
                PRBM = 0
            }
        }
    }
}`

// cT_operating_system_patch_1: Python line 2701
const cT_operating_system_patch_1 = `
// Resource: https://github.com/dortania/Getting-Started-With-ACPI/blob/master/extra-files/decompiled/SSDT-XOSI.dsl

DefinitionBlock ("", "SSDT", 2, "ZPSS", "XOSI", 0x00001000)
{
    Method (XOSI, 1, NotSerialized)
    {
        // Based off of: 
        // https://docs.microsoft.com/en-us/windows-hardware/drivers/acpi/winacpi-osi#_osi-strings-for-windows-operating-systems
        // Add OSes from the below list as needed, most only check up to Windows 2015
        // but check what your DSDT looks for
        Store (Package ()
        {
[[OSIStrings]]
        }, Local0)
        If (_OSI ("Darwin"))
        {
            Return (LNotEqual (Match (Local0, MEQ, Arg0, MTR, Zero, Zero), Ones))
        }
        Else
        {
            Return (_OSI (Arg0))
        }
    }
}`

// cT_operating_system_patch_2: Python line 2725
const cT_operating_system_patch_2 = `
,`

// ---- helpers (prefix c) -----------------------------------------------------

func cDict(kv ...any) *plist.Dict {
	d := plist.NewDict()
	for i := 0; i+1 < len(kv); i += 2 {
		d.Set(kv[i].(string), kv[i+1])
	}
	return d
}

func cAddEntry(g *Guru, ssdtName, ssdtContent string) (*plist.Dict, error) {
	enabled, err := g.WriteSSDT(ssdtName, ssdtContent, true)
	if err != nil {
		return nil, err
	}
	return cDict("Comment", ssdtName+".aml", "Enabled", enabled, "Path", ssdtName+".aml"), nil
}

func cRandomMAC(g *Guru) string {
	if g.RandomMAC != nil {
		return g.RandomMAC()
	}
	var b strings.Builder
	for i := 0; i < 6; i++ {
		fmt.Fprintf(&b, "%02X", rand.Intn(256))
	}
	return b.String()
}

func cPatch(comment, find, replace string) *plist.Dict {
	return cDict("Comment", comment, "Find", find, "Replace", replace)
}

// EnableBacklightControls mirrors enable_backlight_controls.
func (g *Guru) EnableBacklightControls() (*plist.Dict, error) {
	patches := []any{}

	gpus, err := pyrt.Get(g.HardwareReport, "GPU")
	if err != nil {
		return nil, err
	}
	items, err := pyrt.Items(gpus)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("IndexError: list index out of range")
	}
	integratedGPU := items[len(items)-1].Value
	uidValue := 19
	codename, err := pyrt.Get(integratedGPU, "Codename")
	if err != nil {
		return nil, err
	}
	if pyrt.InStrings(codename, []string{"Iron Lake", "Sandy Bridge", "Ivy Bridge"}) {
		uidValue = 14
	} else if pyrt.InStrings(codename, []string{"Haswell", "Broadwell"}) {
		uidValue = 15
	} else if pyrt.InStrings(codename, []string{"Skylake", "Kaby Lake"}) {
		uidValue = 16
	}

	if strings.Contains(g.Dsdt.Table, "PNLF") {
		patches = append(patches, cPatch("PNLF to XNLF Rename", "504E4C46", "584E4C46"))
	}

	for _, tableName := range SortedNicely(g.Acpi.AcpiTables.Names()) {
		table, _ := g.Acpi.AcpiTables.Get(tableName)
		if bytes.Contains(table.Raw, []byte{0x08, 0x4E, 0x42, 0x43, 0x46, 0x0A, 0x00}) {
			patches = append(patches, cPatch("NBCF 0x00 to 0x01", "084E4243460A00", "084E4243460A01"))
			break
		} else if bytes.Contains(table.Raw, []byte{0x08, 0x4E, 0x42, 0x43, 0x46, 0x00}) {
			patches = append(patches, cPatch("NBCF Zero to One", "084E42434600", "084E42434601"))
			break
		}
	}

	ssdtName := "SSDT-PNLF"
	ssdtContent := cT_enable_backlight_controls_1
	acpiPath, err := pyrt.Get(integratedGPU, "ACPI Path")
	if err != nil {
		return nil, err
	}
	hasPath := pyrt.Truthy(acpiPath)
	if hasPath {
		ssdtContent += cT_enable_backlight_controls_2
	} else {
		ssdtContent += cT_enable_backlight_controls_3
	}
	ssdtContent += cT_enable_backlight_controls_4
	if hasPath && uidValue == 14 {
		ssdtContent += cT_enable_backlight_controls_5
	}
	ssdtContent += cT_enable_backlight_controls_6

	ssdtContent = strings.ReplaceAll(ssdtContent, "[[uid_value]]", strconv.Itoa(uidValue))
	if hasPath {
		p, err := pyrt.Str(acpiPath)
		if err != nil {
			return nil, err
		}
		ssdtContent = strings.ReplaceAll(ssdtContent, "[[DevicePath]]", p)
	}

	entry, err := cAddEntry(g, ssdtName, ssdtContent)
	if err != nil {
		return nil, err
	}
	return cDict("Add", []any{entry}, "Patch", patches), nil
}

// EnableGPIODevice mirrors enable_gpio_device; nil means None.
func (g *Guru) EnableGPIODevice() (*plist.Dict, error) {
	// try: ...[0][0] or ...[0][0]; except: return
	var gpioDevice string
	if p := g.Acpi.GetDevicePaths("GPI0", g.Dsdt); len(p) > 0 {
		gpioDevice = p[0].Path
		if gpioDevice == "" {
			if p2 := g.Acpi.GetDevicePaths("GPIO", g.Dsdt); len(p2) > 0 {
				gpioDevice = p2[0].Path
			} else {
				return nil, nil
			}
		}
	} else {
		return nil, nil
	}

	parts := strings.Split(gpioDevice, ".")
	sta, err := g.GetStaVar("", gpioDevice, "", parts[len(parts)-1], false, g.Dsdt)
	if err != nil {
		return nil, err
	}
	staType := sta.StaType
	if staType == "" {
		staType = "MethodObj"
	}
	xsta := gpioDevice + ".XSTA"
	if staType == "MethodObj" {
		xsta += " ()"
	}

	ssdtName := "SSDT-GPI0"
	ssdtContent := cT_enable_gpio_device_1
	ssdtContent = strings.ReplaceAll(ssdtContent, "[[GPI0Path]]", gpioDevice)
	ssdtContent = strings.ReplaceAll(ssdtContent, "[[STAType]]", staType)
	ssdtContent = strings.ReplaceAll(ssdtContent, "[[XSTA]]", xsta)

	entry, err := cAddEntry(g, ssdtName, ssdtContent)
	if err != nil {
		return nil, err
	}
	return cDict("Add", []any{entry}, "Patch", cStaPatches(sta)), nil
}

func cStaPatches(sta *STAVar) []any {
	out := []any{}
	for _, p := range sta.Patches {
		out = append(out, cPatch(p.Comment, p.Find, p.Replace))
	}
	return out
}

// EnableNVRAMSupport mirrors enable_nvram_support; nil means None.
func (g *Guru) EnableNVRAMSupport() (*plist.Dict, error) {
	if g.LPCBusDevice == "" {
		return nil, nil
	}
	ssdtName := "SSDT-PMC"
	ssdtContent := strings.ReplaceAll(cT_enable_nvram_support_1, "[[LPCPath]]", g.LPCBusDevice)
	entry, err := cAddEntry(g, ssdtName, ssdtContent)
	if err != nil {
		return nil, err
	}
	return cDict("Add", []any{entry}), nil
}

// RemoveConditionalScope mirrors remove_conditional_scope.
func (g *Guru) RemoveConditionalScope() (*plist.Dict, error) {
	return cDict("Patch", []any{
		cDict(
			"Comment", "Remove conditional ACPI scope declaration",
			"Find", "A000000092935043484100",
			"Replace", "A3A3A3A3A3A3A3A3A3A3A3",
			"Mask", "FF000000FFFFFFFFFFFFFF",
			"Count", int64(1),
			"TableSignature", "44534454",
		),
	}), nil
}

// FixHP005PostError mirrors fix_hp_005_post_error; nil means None.
func (g *Guru) FixHP005PostError() (*plist.Dict, error) {
	if bytes.Contains(g.Dsdt.Raw, []byte{0x47, 0x01, 0x70, 0x00, 0x70, 0x00, 0x01, 0x08}) {
		return cDict("Patch", []any{
			cPatch("Fix HP Real-Time Clock Power Loss (005) Post Error", "4701700070000108", "4701700070000102"),
		}), nil
	}
	return nil, nil
}

// AddNullEthernetDevice mirrors add_null_ethernet_device.
func (g *Guru) AddNullEthernetDevice() (*plist.Dict, error) {
	mac := cRandomMAC(g)
	var bytesList []string
	for i := 0; i < len(mac); i += 2 {
		end := i + 2
		if end > len(mac) {
			end = len(mac)
		}
		bytesList = append(bytesList, "0x"+mac[i:end])
	}
	macAddressByte := strings.Join(bytesList, ", ")

	ssdtName := "SSDT-RMNE"
	ssdtContent := strings.ReplaceAll(cT_add_null_ethernet_device_1, "[[MACAddress]]", macAddressByte)
	entry, err := cAddEntry(g, ssdtName, ssdtContent)
	if err != nil {
		return nil, err
	}
	return cDict("Add", []any{entry}), nil
}

// FixSystemClockHEDT mirrors fix_system_clock_hedt; nil means None.
func (g *Guru) FixSystemClockHEDT() (*plist.Dict, error) {
	awacDevice := g.Acpi.GetDevicePathsWithHID("ACPI000E", g.Dsdt)
	var rtcDevice string
	if p := g.Acpi.GetDevicePathsWithHID("PNP0B00", g.Dsdt); len(p) > 0 {
		rtcDevice = p[0].Path
		if strings.HasSuffix(rtcDevice, "RTC") {
			rtcDevice += "_"
		}
	} else {
		if g.LPCBusDevice == "" {
			return nil, nil
		}
		rtcDevice = g.LPCBusDevice + ".RTC0"
	}
	rtcParts := strings.Split(rtcDevice, ".")
	unique, _ := g.GetUniqueDevice(rtcParts[len(rtcParts)-1], 0, nil)
	parentPath := strings.Join(rtcParts[:len(rtcParts)-1], ".")
	newRTCDevice := strings.Join(append(append([]string{}, rtcParts[:len(rtcParts)-1]...), unique), ".")

	patches := []any{}
	ssdtName := "SSDT-RTC0-RANGE"
	ssdtContent := cT_fix_system_clock_hedt_1
	if len(awacDevice) == 0 {
		sta, err := g.GetStaVar("", rtcDevice, "", rtcParts[len(rtcParts)-1], false, g.Dsdt)
		if err != nil {
			return nil, err
		}
		patches = append(patches, cStaPatches(sta)...)
		ssdtContent += strings.ReplaceAll(cT_fix_system_clock_hedt_2, "[[device_path]]", rtcDevice)
	}
	ssdtContent += strings.ReplaceAll(strings.ReplaceAll(cT_fix_system_clock_hedt_3, "[[parent_path]]", parentPath), "[[device_path]]", newRTCDevice)

	entry, err := cAddEntry(g, ssdtName, ssdtContent)
	if err != nil {
		return nil, err
	}
	return cDict("Add", []any{entry}, "Patch", patches), nil
}

// InstantWakeFix mirrors instant_wake_fix; nil means None.
func (g *Guru) InstantWakeFix() (*plist.Dict, error) {
	ssdtName := "SSDT-PRW"
	raw := g.Dsdt.Raw
	patches := []any{}

	gprw := bytes.Contains(raw, []byte{0x47, 0x50, 0x52, 0x57, 0x02})
	if gprw {
		patches = append(patches, cPatch("GPRW to XPRW Rename", "4750525702", "5850525702"))
	}
	uprw := bytes.Contains(raw, []byte{0x55, 0x50, 0x52, 0x57, 0x02})
	if uprw {
		patches = append(patches, cPatch("UPRW to XPRW Rename", "5550525702", "5850525702"))
	}
	uswe := bytes.Contains(raw, []byte{0x93, 0x55, 0x53, 0x57, 0x45})
	wole := bytes.Contains(raw, []byte{0x93, 0x57, 0x4F, 0x4C, 0x45})

	ssdtContent := cT_instant_wake_fix_1
	if gprw || uprw {
		ssdtContent += cT_instant_wake_fix_2
	}
	if uswe {
		ssdtContent += cT_instant_wake_fix_3
	}
	if wole {
		ssdtContent += cT_instant_wake_fix_4
	}
	if uswe || wole {
		ssdtContent += cT_instant_wake_fix_5
		if uswe {
			ssdtContent += cT_instant_wake_fix_6
		}
		if wole {
			ssdtContent += cT_instant_wake_fix_7
		}
		ssdtContent += cT_instant_wake_fix_8
	}
	if gprw {
		ssdtContent += cT_instant_wake_fix_9
	}
	if uprw {
		ssdtContent += cT_instant_wake_fix_10
	}
	ssdtContent += cT_instant_wake_fix_11

	if gprw || uprw || uswe || wole {
		entry, err := cAddEntry(g, ssdtName, ssdtContent)
		if err != nil {
			return nil, err
		}
		return cDict("Add", []any{entry}, "Patch", patches), nil
	}
	return nil, nil
}

// FixUncoreBridge mirrors fix_uncore_bridge; nil means None.
func (g *Guru) FixUncoreBridge() (*plist.Dict, error) {
	if len(g.Acpi.GetDevicePaths("UNC0", g.Dsdt)) == 0 {
		return nil, nil
	}
	ssdtName := "SSDT-UNC"
	entry, err := cAddEntry(g, ssdtName, cT_fix_uncore_bridge_1)
	if err != nil {
		return nil, err
	}
	return cDict("Add", []any{entry}), nil
}

// OperatingSystemPatch mirrors operating_system_patch.
func (g *Guru) OperatingSystemPatch() (*plist.Dict, error) {
	ssdtName := "SSDT-XOSI"
	var osi []string
	for _, o := range g.OSIStrings {
		if strings.Contains(g.Dsdt.Table, o[1]) {
			osi = append(osi, "            \""+o[1]+"\"")
		}
	}
	ssdtContent := strings.ReplaceAll(cT_operating_system_patch_1, "[[OSIStrings]]", strings.Join(osi, cT_operating_system_patch_2))

	patches := []any{}
	if len(g.Acpi.GetMethodPaths("OSID", g.Dsdt)) > 0 {
		patches = append(patches, cPatch("OSID to XSID rename - must come before _OSI to XOSI rename!", "4F534944", "58534944"))
	}
	if len(g.Acpi.GetMethodPaths("OSIF", g.Dsdt)) > 0 {
		patches = append(patches, cPatch("OSIF to XSIF rename - must come before _OSI to XOSI rename!", "4F534946", "58534946"))
	}
	patches = append(patches, cPatch("_OSI to XOSI rename - requires SSDT-XOSI.aml", "5F4F5349", "584F5349"))

	entry, err := cAddEntry(g, ssdtName, ssdtContent)
	if err != nil {
		return nil, err
	}
	return cDict("Add", []any{entry}, "Patch", patches), nil
}
