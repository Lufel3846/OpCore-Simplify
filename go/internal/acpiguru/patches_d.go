package acpiguru

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/dsdt"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyrt"
)

// dAddEntry builds {"Comment", "Enabled", "Path"} for an "Add" list.
func dAddEntry(name string, enabled bool) *plist.Dict {
	e := plist.NewDict()
	e.Set("Comment", name+".aml")
	e.Set("Enabled", enabled)
	e.Set("Path", name+".aml")
	return e
}

// dTail is s[i:] with Python's slice clamping (i >= 0).
func dTail(s string, i int) string {
	if i >= len(s) {
		return ""
	}
	return s[i:]
}

// dSplitLines is str.splitlines().
func dSplitLines(s string) []string {
	var out []string
	start := 0
	i := 0
	for i < len(s) {
		n := 0
		switch c := s[i]; {
		case c == '\r':
			n = 1
			if i+1 < len(s) && s[i+1] == '\n' {
				n = 2
			}
		case c == '\n' || c == '\v' || c == '\f' || (c >= 0x1c && c <= 0x1e):
			n = 1
		case strings.HasPrefix(s[i:], "\u0085"), strings.HasPrefix(s[i:], " "), strings.HasPrefix(s[i:], " "):
			n = len("\u0085")
			if s[i] == 0xe2 {
				n = 3
			}
		}
		if n == 0 {
			i++
			continue
		}
		out = append(out, s[start:i])
		i += n
		start = i
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// dPyInt10 is int(s) for a str: whitespace, sign, ASCII digits with single
// underscores between them. Values beyond int64 saturate.
func dPyInt10(s string) (int64, bool) {
	s = pyStrip(s)
	neg := false
	if strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-") {
		neg = s[0] == '-'
		s = s[1:]
	}
	if s == "" || strings.HasPrefix(s, "_") || strings.HasSuffix(s, "_") || strings.Contains(s, "__") {
		return 0, false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && r != '_' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(strings.ReplaceAll(s, "_", ""), 10, 64)
	if err != nil {
		n = math.MaxInt64
	}
	if neg {
		n = -n
	}
	return n, true
}

// SurfaceLaptopSpecialPatch mirrors surface_laptop_special_patch.
func (g *Guru) SurfaceLaptopSpecialPatch() (*plist.Dict, error) {
	ssdtName := "SSDT-SURFACE"
	ssdtContent := `
DefinitionBlock ("", "SSDT", 2, "ZPSS", "SURFACE", 0x00001000)
{
    External (_SB_.PCI0, DeviceObj)
    External (GPRW, MethodObj)    // 2 Arguments

    If (_OSI ("Darwin"))
    {
        Scope (_SB)
        {
            Device (ALS0)
            {
                Name (_HID, "ACPI0008" /* Ambient Light Sensor Device */)  // _HID: Hardware ID
                Name (_CID, "smc-als")  // _CID: Compatible ID
                Name (_ALI, 0x012C)  // _ALI: Ambient Light Illuminance
                Name (_ALR, Package (0x05)  // _ALR: Ambient Light Response
                {
                    Package (0x02)
                    {
                        0x46, 
                        Zero
                    }, 

                    Package (0x02)
                    {
                        0x49, 
                        0x0A
                    }, 

                    Package (0x02)
                    {
                        0x55, 
                        0x50
                    }, 

                    Package (0x02)
                    {
                        0x64, 
                        0x012C
                    }, 

                    Package (0x02)
                    {
                        0x96, 
                        0x03E8
                    }
                })
                Method (XALI, 1, Serialized)
                {
                    _ALI = Arg0
                }
            }

            Device (ADP0)
            {
                Name (_HID, "ACPI0003" /* Power Source Device */)  // _HID: Hardware ID
                Name (SPSR, Zero)
                Method (_PRW, 0, NotSerialized)  // _PRW: Power Resources for Wake
                {
                    Return (GPRW (0x6D, 0x04))
                }

                Method (_STA, 0, NotSerialized)  // _STA: Status
                {
                    Return (0x0F)
                }

                Method (XPSR, 1, Serialized)
                {
                    If ((Arg0 == Zero))
                    {
                        SPSR = Zero
                    }
                    ElseIf ((Arg0 == One))
                    {
                        SPSR = One
                    }

                    Notify (ADP0, 0x80) // Status Change
                }

                Method (_PSR, 0, Serialized)  // _PSR: Power Source
                {
                    Return (SPSR) /* \_SB_.ADP0.SPSR */
                }

                Method (_PCL, 0, NotSerialized)  // _PCL: Power Consumer List
                {
                    Return (\_SB)
                }
            }

            Device (BAT0)
            {
                Name (_HID, EisaId ("PNP0C0A") /* Control Method Battery */)  // _HID: Hardware ID
                Name (_UID, Zero)  // _UID: Unique ID
                Name (_PCL, Package (0x01)  // _PCL: Power Consumer List
                {
                    _SB
                })
                Method (_STA, 0, NotSerialized)  // _STA: Status
                {
                    Return (0x1F)
                }
            }
        }

        Scope (_SB.PCI0)
        {
            Device (IPTS)
            {
                Name (_ADR, 0x00160004)  // _ADR: Address
            }
        }
    }
}
`
	ok, err := g.WriteSSDT(ssdtName, ssdtContent, true)
	if err != nil {
		return nil, err
	}
	d := plist.NewDict()
	d.Set("Add", []any{dAddEntry(ssdtName, ok)})
	return d, nil
}

// FindLineStart mirrors find_line_start.
func (g *Guru) FindLineStart(text string, index int) int {
	cur := index
	for cur > 0 {
		if text[cur] == '\n' {
			return cur + 1
		}
		cur--
	}
	return 0
}

// ExtractLine mirrors extract_line: the stripped line, its start and its end.
func (g *Guru) ExtractLine(text string, index int) (string, int, int) {
	start := g.FindLineStart(text, index)
	end := len(text)
	if i := strings.Index(text[start:], "\n"); i >= 0 {
		end = start + i + 1
	}
	return pyStrip(text[start:end]), start, end
}

// ExtractBlockContent mirrors extract_block_content.
func (g *Guru) ExtractBlockContent(text string, startIdx int) string {
	i := strings.Index(dTail(text, startIdx), "{")
	if i < 0 {
		return ""
	}
	blockStart := dMax0(startIdx) + i
	count := 1
	pos := blockStart + 1
	for count > 0 && pos < len(text) {
		if text[pos] == '{' {
			count++
		} else if text[pos] == '}' {
			count--
		}
		pos++
	}
	if count == 0 {
		return text[blockStart:pos]
	}
	return ""
}

func dMax0(i int) int {
	if i < 0 {
		return 0
	}
	return i
}

// ParseFieldLine mirrors parse_field_line; ok is false where it returns (None, None).
func (g *Guru) ParseFieldLine(line string) (string, int64, bool) {
	if strings.Contains(line, "//") {
		line = pyStrip(strings.Split(line, "//")[0])
	}
	parts := strings.Split(line, ",")
	if len(parts) >= 2 {
		name := pyStrip(parts[0])
		size, ok := dPyInt10(parts[1])
		if !ok {
			return "", 0, false
		}
		return name, size, true
	}
	return "", 0, false
}

// ProcessEmbeddedControlRegion mirrors process_embedded_control_region; an empty
// name is Python's None.
func (g *Guru) ProcessEmbeddedControlRegion(table string, startIdx int) (string, int) {
	i := strings.Index(dTail(table, startIdx), "EmbeddedControl")
	if i < 0 {
		return "", startIdx + 1
	}
	line, _, endLine := g.ExtractLine(table, dMax0(startIdx)+i)
	p := strings.Split(line, "(")
	if len(p) < 2 {
		return "", startIdx + 1
	}
	return pyStrip(strings.Split(p[1], ",")[0]), endLine
}

// DField is one {"name", "size"} entry of process_field_definition.
type DField struct {
	Name string
	Size int64
}

// ProcessFieldDefinition mirrors process_field_definition.
func (g *Guru) ProcessFieldDefinition(table, regionName string, startIdx int) ([]DField, int) {
	var fields []DField
	pattern := "Field (" + regionName
	if !strings.Contains(dTail(table, startIdx), pattern) {
		return fields, len(table)
	}
	fieldStart := dMax0(startIdx) + strings.Index(dTail(table, startIdx), pattern)
	_, _, fieldEnd := g.ExtractLine(table, fieldStart)
	block := g.ExtractBlockContent(table, fieldEnd)
	for _, line := range dSplitLines(block) {
		line = pyStrip(line)
		if line == "" || line == "{" || line == "}" {
			continue
		}
		name, size, ok := g.ParseFieldLine(line)
		if ok && name != "" {
			fields = append(fields, DField{Name: name, Size: size})
		}
	}
	return fields, fieldEnd
}

// BatteryStatusPatch mirrors battery_status_patch.
func (g *Guru) BatteryStatusPatch() (bool, error) {
	if g.Dsdt == nil {
		return false, nil
	}
	table := g.Dsdt.Table
	searchStart := 0
	var all []DField
	for strings.Contains(dTail(table, searchStart), "EmbeddedControl") {
		var region string
		region, searchStart = g.ProcessEmbeddedControlRegion(table, searchStart)
		if region == "" {
			continue
		}
		cur := searchStart
		var regionFields []DField
		for {
			fields, next := g.ProcessFieldDefinition(table, region, cur)
			if len(fields) == 0 || next <= cur {
				break
			}
			regionFields = append(regionFields, fields...)
			cur = next
			if !strings.Contains(dTail(table, cur), "Field ("+region) {
				break
			}
		}
		all = append(all, regionFields...)
	}
	for _, f := range all {
		if f.Size > 8 {
			return true, nil
		}
	}
	return false, nil
}

// FixAPICProcessorID mirrors fix_apic_processor_id.
func (g *Guru) FixAPICProcessorID() (*plist.Dict, error) {
	apic := g.Acpi.GetTableWithSignature([]byte("APIC"))
	newAPIC := ""
	if apic == nil {
		return nil, nil
	}
	for _, tableName := range SortedNicely(g.Acpi.AcpiTables.Names()) {
		table, _ := g.Acpi.AcpiTables.Get(tableName)
		processors := g.Acpi.GetProcessorPaths("Processor", table)
		if len(processors) == 0 {
			continue
		}
		processorIndex := -1
		apicLength := len(apic.Lines)
		skipUnknown := false
		for index := 0; index < apicLength; index++ {
			line := apic.Lines[index]
			if strings.Contains(line, "Unknown") {
				skipUnknown = !skipUnknown
				continue
			}
			if skipUnknown {
				continue
			}
			if strings.Contains(line, "Subtable Type") && strings.Contains(line, "[Processor Local APIC]") {
				processorIndex++
				if index+2 >= len(apic.Lines) {
					return nil, errors.New("IndexError: list index out of range")
				}
				cur := apic.Lines[index+2]
				apicProcessorID := cur
				if len(cur) > 2 {
					apicProcessorID = cur[len(cur)-2:]
				}
				processorID, ok := dProcessorID(table.Lines, processors, processorIndex)
				if !ok {
					return nil, nil
				}
				if processorIndex == 0 && apicProcessorID == processorID {
					return nil, nil
				}
				head := ""
				if len(cur) > 2 {
					head = cur[:len(cur)-2]
				}
				apic.Lines[index+2] = head + processorID
			}
			newAPIC += line + "\n"
		}
		if processorIndex != -1 {
			ok, err := g.WriteSSDT("APIC", newAPIC, true)
			if err != nil {
				return nil, err
			}
			_ = ok
			// The original calls self.dropping_the_table("APIC") with a str, so
			// (signature or oemtableid).rstrip(b"\x00") raises TypeError.
			return nil, fmt.Errorf("%w: rstrip arg must be None or str", pyrt.ErrType)
		}
	}
	return nil, nil
}

// dProcessorID is the try block of fix_apic_processor_id; ok is false when it
// raises (the bare except returns None).
func dProcessorID(lines []string, processors []dsdt.Path, idx int) (string, bool) {
	if idx >= len(processors) {
		return "", false
	}
	li := processors[idx].Index
	if li >= len(lines) || li < -len(lines) {
		return "", false
	}
	if li < 0 {
		li += len(lines)
	}
	parts := strings.Split(lines[li], ", ")
	if len(parts) < 2 {
		return "", false
	}
	return dTail(parts[1], 2), true
}

// DisableUSBHubDevices mirrors disable_usb_hub_devices.
func (g *Guru) DisableUSBHubDevices() (*plist.Dict, error) {
	ssdtName := "SSDT-USB-Reset"
	patches := []any{}
	ssdtContent := `
DefinitionBlock ("", "SSDT", 2, "ZPSS", "UsbReset", 0x00001000)
{`

	rhub := g.Acpi.GetDevicePaths("RHUB", nil)
	rhub = append(rhub, g.Acpi.GetDevicePaths("HUBN", nil)...)
	rhub = append(rhub, g.Acpi.GetDevicePaths("URTH", nil)...)
	if len(rhub) == 0 {
		return nil, nil
	}
	for _, device := range rhub {
		devicePath := device.Path
		parts := strings.Split(devicePath, ".")
		sta, err := g.GetStaVar("", devicePath, "", parts[len(parts)-1], false, g.Dsdt)
		if err != nil {
			return nil, err
		}
		for _, p := range sta.Patches {
			e := plist.NewDict()
			e.Set("Comment", p.Comment)
			e.Set("Find", p.Find)
			e.Set("Replace", p.Replace)
			patches = append(patches, e)
		}
		ssdtContent += strings.ReplaceAll(`
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
    }
`, "[[device_path]]", devicePath)
	}
	ssdtContent += "\n}"

	ok, err := g.WriteSSDT(ssdtName, ssdtContent, true)
	if err != nil {
		return nil, err
	}
	d := plist.NewDict()
	d.Set("Add", []any{dAddEntry(ssdtName, ok)})
	d.Set("Patch", patches)
	return d, nil
}

// ReturnThermalZone mirrors return_thermal_zone.
func (g *Guru) ReturnThermalZone() (*plist.Dict, error) {
	ssdtName := "SSDT-WMIS"
	ssdtContent := `
// Resource: https://github.com/zhen-zen/YogaSMC/blob/master/YogaSMC/SSDTSample/SSDT-WMIS.dsl

/*
 * Sample SSDT to fix sensor return
 *
 * Certain models forget to return result from ThermalZone:
 *
 * Method (WQBI, 1, NotSerialized)
 * {
 *     \_TZ.WQBI (Arg0)
 * }
 *
 * So we have to patch it for correct reporting.
 * Rename Method (WQBI, 1, N) to XQBI
 * (ThermalZone one usually has Serialized type)
 *
 * Find: 57514249 01 // WQBI
 * Repl: 58514249 01 // XQBI 
 *
 * MethodFlags :=
 * bit 0-2: ArgCount (0-7)
 * bit 3:   SerializeFlag
 *          0 NotSerialized
 *          1 Serialized
 */
DefinitionBlock ("", "SSDT", 2, "ZPSS", "WMIS", 0x00000000)
{
    External (_TZ.WQBI, MethodObj)    // Method in ThermalZone

    Method (_SB.WMIS.WQBI, 1, NotSerialized)
    {
        Return (\_TZ.WQBI (Arg0))
    }
}
`
	for _, tableName := range SortedNicely(g.Acpi.AcpiTables.Names()) {
		table, _ := g.Acpi.AcpiTables.Get(tableName)
		if len(g.Acpi.GetMethodPaths("WQBI", table)) == 0 {
			continue
		}
		ok, err := g.WriteSSDT(ssdtName, ssdtContent, true)
		if err != nil {
			return nil, err
		}
		p := plist.NewDict()
		p.Set("Comment", "WQBI to XQBI Rename")
		p.Set("Find", "5751424901")
		p.Set("Replace", "5851424901")
		d := plist.NewDict()
		d.Set("Add", []any{dAddEntry(ssdtName, ok)})
		d.Set("Patch", []any{p})
		return d, nil
	}
	return nil, nil
}
