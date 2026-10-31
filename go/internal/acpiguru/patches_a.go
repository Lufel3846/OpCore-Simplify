package acpiguru

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/dsdt"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyrt"
)

// aPatchDict builds a {"Comment", "Find", "Replace"} patch entry.
func aPatchDict(p STAPatch) *plist.Dict {
	d := plist.NewDict()
	d.Set("Comment", p.Comment)
	d.Set("Find", p.Find)
	d.Set("Replace", p.Replace)
	return d
}

// aAddDict builds the {"Add": [...]} dict shared by the SSDT patches; patches
// is nil for the ones that return no "Patch" key.
func aAddDict(ssdtName string, enabled bool) *plist.Dict {
	entry := plist.NewDict()
	entry.Set("Comment", ssdtName+".aml")
	entry.Set("Enabled", enabled)
	entry.Set("Path", ssdtName+".aml")
	d := plist.NewDict()
	d.Set("Add", []any{entry})
	return d
}

// EnableCPUPowerManagement mirrors enable_cpu_power_management. A nil dict is
// Python's None.
func (g *Guru) EnableCPUPowerManagement() (*plist.Dict, error) {
	for _, tableName := range SortedNicely(g.Acpi.AcpiTables.Names()) {
		ssdtName := "SSDT-PLUG"
		table, _ := g.Acpi.AcpiTables.Get(tableName)
		if sig := string(table.Signature); sig != "DSDT" && sig != "SSDT" {
			continue // We're not checking data tables
		}
		cpuName := ""
		if procs := g.Acpi.GetProcessorPaths("Processor", table); len(procs) > 0 {
			cpuName = procs[0].Path
		}
		var ssdt string
		if cpuName != "" {
			ssdt = strings.ReplaceAll(`//
// Based on the sample found at https://github.com/acidanthera/OpenCorePkg/blob/master/Docs/AcpiSamples/SSDT-PLUG.dsl
//
DefinitionBlock ("", "SSDT", 2, "ZPSS", "CpuPlug", 0x00003000)
{
    External ([[CPUName]], ProcessorObj)
    Scope ([[CPUName]])
    {
        If (_OSI ("Darwin")) {
            Method (_DSM, 4, NotSerialized)  // _DSM: Device-Specific Method
            {
                If (LNot (Arg2))
                {
                    Return (Buffer (One)
                    {
                        0x03
                    })
                }
                Return (Package (0x02)
                {
                    "plugin-type", 
                    One
                })
            }
        }
    }
}`, "[[CPUName]]", cpuName)
		} else {
			ssdtName += "-ALT"
			procs := g.Acpi.GetDevicePathsWithHID("ACPI0007", table)
			if len(procs) == 0 {
				continue
			}
			parent := strings.Split(procs[0].Path, ".")[0]
			type procUID struct{ proc, uid string }
			var procList []procUID
			for _, proc := range procs {
				uid := g.Acpi.GetPathOfType("Name", proc.Path+"._UID", table)
				if len(uid) == 0 {
					continue
				}
				// Let's get the actual _UID value
				if uid[0].Index < 0 || uid[0].Index >= len(table.Lines) {
					continue
				}
				parts := strings.Split(table.Lines[uid[0].Index], "_UID, ")
				if len(parts) < 2 {
					continue
				}
				procList = append(procList, procUID{proc.Path, strings.Split(parts[1], ")")[0]})
			}
			if len(procList) == 0 {
				continue
			}
			ssdt = strings.ReplaceAll(`//
// Based on the sample found at https://github.com/acidanthera/OpenCorePkg/blob/master/Docs/AcpiSamples/Source/SSDT-PLUG-ALT.dsl
//
DefinitionBlock ("", "SSDT", 2, "ZPSS", "CpuPlugA", 0x00003000)
{
    External ([[parent]], DeviceObj)

    Scope ([[parent]])
    {`, "[[parent]]", parent)
			// Ensure our name scheme won't conflict
			schemes := []string{"C000", "CP00", "P000", "PR00", "CX00", "PX00"}
			// Walk the processor objects, and add them to the SSDT
			for i, pu := range procList {
				adr := strings.ToUpper(strconv.FormatInt(int64(i), 16))
				name := ""
				for _, s := range schemes {
					cut := len(s) - len(adr)
					if cut < 0 {
						cut = 0
					}
					nameCheck := s[:cut] + adr
					checkPath := parent + "." + nameCheck
					if len(g.Acpi.GetPathOfType("Device", checkPath, table)) > 0 {
						continue // Already defined - skip
					}
					// If we got here - we found an unused name
					name = nameCheck
					break
				}
				if name == "" {
					return nil, nil
				}
				ssdt += strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(`
        Processor ([[name]], [[uid]], 0x00000510, 0x06)
        {
            // [[proc]]
            Name (_HID, "ACPI0007" /* Processor Device */)  // _HID: Hardware ID
            Name (_UID, [[uid]])
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
            }`, "[[name]]", name), "[[uid]]", pu.uid), "[[proc]]", pu.proc)
				if i == 0 { // Got the first, add plugin-type as well
					ssdt += `
            Method (_DSM, 4, NotSerialized)
            {
                If (LNot (Arg2)) {
                    Return (Buffer (One) { 0x03 })
                }

                Return (Package (0x02)
                {
                    "plugin-type",
                    One
                })
            }`
				}
				// Close up the SSDT
				ssdt += `
        }`
			}
			ssdt += `
    }
}`
		}
		enabled, err := g.WriteSSDT(ssdtName, ssdt, true)
		if err != nil {
			return nil, err
		}
		return aAddDict(ssdtName, enabled), nil
	}
	return nil, nil
}

// IRQDeviceInfo is one value of the dict list_irqs returns.
type IRQDeviceInfo struct {
	IRQ    string
	HID    string
	HasHID bool
}

// IRQDevices is the insertion-ordered dict returned by list_irqs.
type IRQDevices struct {
	Order []string
	M     map[string]*IRQDeviceInfo
}

// ListIRQs mirrors list_irqs.
func (g *Guru) ListIRQs() (*IRQDevices, error) {
	if g.Dsdt == nil {
		return nil, fmt.Errorf("%w: 'NoneType' object is not subscriptable", pyrt.ErrType)
	}
	devices := &IRQDevices{M: map[string]*IRQDeviceInfo{}}
	currentDevice := ""
	currentHID := ""
	irq := false
	lastIRQ := false
	irqIndex := 0
	for index, line := range g.Dsdt.Lines {
		if g.Acpi.IsHex(line) {
			// Skip all hex lines
			continue
		}
		switch {
		case irq:
			// Get the values
			parts := strings.Split(line, "{")
			if len(parts) < 2 {
				return nil, fmt.Errorf("IndexError: list index out of range")
			}
			num := strings.ReplaceAll(strings.Split(parts[1], "}")[0], " ", "")
			if num == "" {
				num = "#"
			}
			if dev, ok := devices.M[currentDevice]; ok {
				if lastIRQ { // In a row
					dev.IRQ += ":" + num
				} else { // Skipped at least one line
					_, irqIndex, _ = g.Acpi.FindNextHex(index, nil)
					dev.IRQ += "-" + strconv.Itoa(irqIndex) + "|" + num
				}
			} else {
				_, irqIndex, _ = g.Acpi.FindNextHex(index, nil)
				devices.Order = append(devices.Order, currentDevice)
				devices.M[currentDevice] = &IRQDeviceInfo{IRQ: strconv.Itoa(irqIndex) + "|" + num}
			}
			irq = false
			lastIRQ = true
		case strings.Contains(line, "Device ("):
			// Check if we retain the _HID here
			if dev, ok := devices.M[currentDevice]; ok && currentDevice != "" && currentHID != "" {
				// Save it
				dev.HID = currentHID
				dev.HasHID = true
			}
			lastIRQ = false
			currentHID = ""
			currentDevice = strings.Split(strings.Split(line, "(")[1], ")")[0]
		case strings.Contains(line, "_HID, ") && currentDevice != "":
			if parts := strings.Split(line, `"`); len(parts) > 1 {
				currentHID = parts[1]
			}
		case strings.Contains(line, "IRQNoFlags") && currentDevice != "":
			// Next line has our interrupts
			irq = true
		default:
			// Check if just a filler line
			r := strings.NewReplacer("{", "", "}", "", "(", "", ")", "", " ", "")
			if len(strings.Split(r.Replace(line), "//")[0]) > 0 {
				// Reset last IRQ as it's not in a row
				lastIRQ = false
			}
		}
	}
	// Retain the final _HID if needed
	if dev, ok := devices.M[currentDevice]; ok && currentDevice != "" && currentHID != "" {
		dev.HID = currentHID
		dev.HasHID = true
	}
	return devices, nil
}

// GetIRQChoice mirrors get_irq_choice: device name -> IRQs to remove.
func (g *Guru) GetIRQChoice(irqs *IRQDevices) map[string][]int {
	namesAndHIDs := []string{"PIC", "IPIC", "TMR", "TIMR", "RTC", "RTC0", "RTC1", "PNPC0000", "PNP0100", "PNP0B00"}
	d := map[string][]int{}
	for _, x := range irqs.Order {
		if hasString(namesAndHIDs, strings.ToUpper(x)) || hasString(namesAndHIDs, strings.ToUpper(irqs.M[x].HID)) {
			d[x] = g.TargetIRQs
		}
	}
	return d
}

// IRQHex is one dict of get_hex_from_irqs.
type IRQHex struct {
	IRQ     string
	Find    string
	Repl    string
	Remd    []int
	Index   int
	Changed bool
}

// GetHexFromIRQs mirrors get_hex_from_irqs.
func (g *Guru) GetHexFromIRQs(irq string, remIRQ []int) ([]*IRQHex, error) {
	var lines []*IRQHex
	var remd []int
	for _, a := range strings.Split(irq, "-") {
		parts := strings.Split(a, "|") // Get the index
		if len(parts) != 2 {
			return nil, fmt.Errorf("ValueError: wrong number of values to unpack")
		}
		idx, ok := aPyInt(parts[0])
		if !ok {
			return nil, fmt.Errorf("ValueError: invalid literal for int() with base 10: %q", parts[0])
		}
		i := parts[1]
		find := g.GetIntForLine(i)
		repl := make([]int64, len(find))
		// Now we need to verify if we're patching *all* IRQs, or just some specifics
		if len(remIRQ) > 0 {
			repl = append([]int64{}, find...)
			for _, x := range remIRQ {
				// Get the int
				rem := g.ConvertIRQToInt(x)
				repl1 := make([]int64, len(repl))
				changed := false
				for k, y := range repl {
					if y >= rem {
						repl1[k] = y & (rem ^ 0xFFFF)
					} else {
						repl1[k] = y
					}
					if repl1[k] != y {
						changed = true
					}
				}
				if changed {
					// Changes were made
					remd = append(remd, x)
				}
				repl = repl1
			}
		}
		// Get the hex
		var fb, rb strings.Builder
		for _, x := range find {
			fb.WriteString("22" + dsdt.GetHexFromInt(x, 4))
		}
		for _, x := range repl {
			rb.WriteString("22" + dsdt.GetHexFromInt(x, 4))
		}
		lines = append(lines, &IRQHex{IRQ: i, Find: fb.String(), Repl: rb.String(), Index: int(idx), Changed: fb.String() != rb.String()})
	}
	// Every dict shares the same remd list in the original.
	for _, d := range lines {
		d.Remd = remd
	}
	return lines, nil
}

// aPyInt is int(s) for base 10, limited to what fits an int64.
func aPyInt(s string) (int64, bool) {
	v, err := pyrt.Int(s)
	if err != nil {
		return 0, false
	}
	n, ok := v.(int64)
	return n, ok
}

// GetIntForLine mirrors get_int_for_line.
func (g *Guru) GetIntForLine(irq string) []int64 {
	var irqList []int64
	for _, i := range strings.Split(irq, ":") {
		irqList = append(irqList, g.SameLineIRQ(i))
	}
	return irqList
}

// ConvertIRQToInt mirrors convert_irq_to_int: "0"*(16-irq)+"1"+"0"*irq in base 2.
func (g *Guru) ConvertIRQToInt(irq int) int64 {
	if irq < 0 {
		return 1
	}
	if irq > 62 {
		return math.MaxInt64 // Python's int is unbounded; callers only compare with 16-bit values
	}
	return int64(1) << uint(irq)
}

// SameLineIRQ mirrors same_line_irq.
func (g *Guru) SameLineIRQ(irq string) int64 {
	// We sum the IRQ values and return the int
	var total int64
	for _, i := range strings.Split(irq, ",") {
		if i == "#" {
			continue // Null value
		}
		n, ok := aPyInt(i)
		if !ok {
			// Not an int (or too large to be in range)
			continue
		}
		if n > 15 || n < 0 {
			continue // Out of range
		}
		total |= g.ConvertIRQToInt(int(n))
	}
	return total
}

// aPySetOrder returns the iteration order of Python's set(vals) for
// non-negative small ints, reproducing CPython's open-addressing table.
func aPySetOrder(vals []int) []int {
	type slot struct {
		v    int
		used bool
	}
	mask := 7
	table := make([]slot, mask+1)
	fill := 0
	insert := func(tbl []slot, mask, v int, checkDup bool) bool {
		h := v
		if v == -1 {
			h = -2 // hash(-1) is -2 in CPython
		}
		i := h & mask
		perturb := uint(h)
		for {
			probes := 0
			if i+9 <= mask {
				probes = 9
			}
			e := i
			for {
				if !tbl[e].used {
					tbl[e] = slot{v, true}
					return true
				}
				if checkDup && tbl[e].v == v {
					return false
				}
				e++
				if probes == 0 {
					break
				}
				probes--
			}
			perturb >>= 5
			i = int((uint(i)*5 + 1 + perturb)) & mask
		}
	}
	for _, v := range vals {
		if !insert(table, mask, v, true) {
			continue
		}
		fill++
		if fill*5 < mask*3 {
			continue
		}
		newsize := 8
		for newsize <= fill*4 {
			newsize <<= 1
		}
		nt := make([]slot, newsize)
		for _, s := range table {
			if s.used {
				insert(nt, newsize-1, s.v, false)
			}
		}
		table, mask = nt, newsize-1
	}
	var out []int
	for _, s := range table {
		if s.used {
			out = append(out, s.v)
		}
	}
	return out
}

func aJoinSet(vals []int) string {
	var parts []string
	for _, v := range aPySetOrder(vals) {
		parts = append(parts, strconv.Itoa(v))
	}
	return strings.Join(parts, ",")
}

// FixIRQConflicts mirrors fix_irq_conflicts. A nil dict is Python's None.
func (g *Guru) FixIRQConflicts() (*plist.Dict, error) {
	hpets := g.Acpi.GetDevicePathsWithHID("PNP0103", nil)
	hpetFake := len(hpets) == 0
	hpetSTA := false
	var sta *STAVar
	var patches []any
	var name string
	var hpet []dsdt.Path
	memBase, memLength := "", ""
	gotMem := false
	if len(hpets) > 0 {
		name = hpets[0].Path

		var err error
		sta, err = g.GetStaVar("", "", "PNP0103", "HPET", false, nil)
		if err != nil {
			return nil, err
		}
		if len(sta.Patches) > 0 {
			hpetSTA = true
			for _, p := range sta.Patches {
				patches = append(patches, aPatchDict(p))
			}
		}

		hpet = g.Acpi.GetMethodPaths(name+"._CRS", nil)
		if len(hpet) == 0 {
			hpet = g.Acpi.GetNamePaths(name+"._CRS", nil)
		}
		if len(hpet) == 0 {
			return nil, nil
		}

		_, crsIndex, _ := g.Acpi.FindNextHex(hpet[0].Index, nil)

		primed := false
		for _, line := range g.Acpi.GetScope(hpets[0].Index, false, true, nil) {
			if strings.Contains(line, "Memory32Fixed (") {
				primed = true
				continue
			}
			if !primed {
				continue
			} else if strings.Contains(line, ")") { // Reached the end of the scope
				break
			}
			// We're primed, and not at the end - let's try to get the base and length
			val := strings.Split(pyStrip(line), ",")[0]
			val = strings.ReplaceAll(strings.ReplaceAll(val, "Zero", "0x0"), "One", "0x1")
			if _, ok := pyIntBase16(val); !ok {
				break
			}
			// Set them in order
			if memBase == "" {
				memBase = val
			} else {
				memLength = val
				break // Leave after we get both values
			}
		}
		// Check if we found the values
		gotMem = memBase != "" && memLength != ""
		if !gotMem {
			memBase = "0xFED00000"
			memLength = "0x00000400"
		}
		const crs, xcrs = "5F435253", "58435253"
		pad, err := g.Acpi.GetShortestUniquePad(crs, crsIndex, 0, nil)
		if err != nil {
			return nil, err
		}
		if pad == nil {
			return nil, fmt.Errorf("TypeError: cannot unpack non-iterable NoneType object")
		}
		parts := strings.Split(name, ".")
		patches = append(patches, aPatchDict(STAPatch{
			Comment: strings.TrimLeft(parts[len(parts)-1], "\\") + " _CRS to XCRS Rename",
			Find:    pad.Left + crs + pad.Right,
			Replace: pad.Left + xcrs + pad.Right,
		}))
	} else {
		haveName := false
		if ecList := g.Acpi.GetDevicePathsWithHID("PNP0C09", nil); len(ecList) > 0 {
			parts := strings.Split(ecList[0].Path, ".")
			name = strings.Join(parts[:len(parts)-1], ".")
			haveName = true
		}
		if !haveName {
			for _, x := range []string{"LPCB", "LPC0", "LPC", "SBRG", "PX40"} {
				if paths := g.Acpi.GetDevicePaths(x, nil); len(paths) > 0 {
					name = paths[0].Path
					haveName = true
					break
				}
			}
		}
		if name == "" {
			return nil, nil
		}
	}

	devs, err := g.ListIRQs()
	if err != nil {
		return nil, err
	}
	targetIRQs := g.GetIRQChoice(devs)
	// Let's apply patches as we go
	type uniquePatch struct{ dev, remd, orig, find, repl string }
	type genericPatch struct{ remd, orig, find, repl string }
	uniquePatches := map[string][]uniquePatch{}
	var uniqueOrder []string
	var genericPatches []genericPatch
	for _, dev := range devs.Order {
		target, ok := targetIRQs[dev]
		if !ok {
			continue
		}
		irqPatches, err := g.GetHexFromIRQs(devs.M[dev].IRQ, target)
		if err != nil {
			return nil, err
		}
		for _, t := range irqPatches {
			if !t.Changed {
				// Nothing patched - skip
				continue
			}
			// Try our endings here - 7900, 8609, and 4701 - also allow for up to 8 chars of pad (thanks MSI)
			re, err := regexp.Compile("(" + t.Find + "(.{0,8})(7900|4701|8609))")
			if err != nil {
				return nil, err
			}
			hexStr, _ := g.Acpi.GetHexStartingAt(t.Index, nil)
			matches := re.FindAllStringSubmatch(hexStr, -1)
			if len(matches) == 0 {
				continue
			}
			if len(matches) > 1 {
				// Found too many matches!
				// Add them all as find/replace entries
				for _, x := range matches {
					generic := genericPatch{
						remd: aJoinSet(t.Remd),
						orig: t.Find,
						find: t.Find + x[2] + x[3],
						repl: t.Repl + x[2] + x[3],
					}
					genericPatches = append(genericPatches, generic)
				}
				continue
			}
			ending := matches[0][2] + matches[0][3]
			pad, err := g.Acpi.GetShortestUniquePad(t.Find+ending, t.Index, 0, nil)
			if err != nil {
				return nil, err
			}
			if pad == nil {
				return nil, fmt.Errorf("TypeError: cannot unpack non-iterable NoneType object")
			}
			if _, ok := uniquePatches[dev]; !ok {
				uniqueOrder = append(uniqueOrder, dev)
			}
			uniquePatches[dev] = append(uniquePatches[dev], uniquePatch{
				dev:  dev,
				remd: aJoinSet(t.Remd),
				orig: t.Find,
				find: pad.Left + t.Find + ending + pad.Right,
				repl: pad.Left + t.Repl + ending + pad.Right,
			})
		}
	}
	// Walk the unique patches if any
	for _, x := range uniqueOrder {
		for i, p := range uniquePatches[x] {
			patchName := fmt.Sprintf("%s IRQ %s Patch", x, p.remd)
			if len(uniquePatches[x]) > 1 {
				patchName += fmt.Sprintf(" - %d of %d", i+1, len(uniquePatches[x]))
			}
			patches = append(patches, aPatchDict(STAPatch{Comment: patchName, Find: p.find, Replace: p.repl}))
		}
	}
	// Walk the generic patches if any
	if len(genericPatches) > 0 {
		var genericSet []genericPatch // Make sure we don't repeat find values
		for _, x := range genericPatches {
			dup := false
			for _, y := range genericSet {
				if x == y {
					dup = true
					break
				}
			}
			if dup {
				continue
			}
			genericSet = append(genericSet, x)
		}
		for i, x := range genericSet {
			patchName := fmt.Sprintf("Generic IRQ Patch %d of %d - %s - %s", i+1, len(genericSet), x.remd, x.orig)
			d := aPatchDict(STAPatch{Comment: patchName, Find: x.find, Replace: x.repl})
			d.Set("Enabled", false)
			patches = append(patches, d)
		}
	}

	ssdtName := "SSDT-HPET"
	var ssdtContent string
	if hpetFake {
		ssdtContent = strings.ReplaceAll(`// Fake HPET device
//
DefinitionBlock ("", "SSDT", 2, "ZPSS", "HPET", 0x00000000)
{
    External ([[name]], DeviceObj)

    Scope ([[name]])
    {
        Device (HPET)
        {
            Name (_HID, EisaId ("PNP0103") /* HPET System Timer */)  // _HID: Hardware ID
            Name (_CID, EisaId ("PNP0C01") /* System Board */)  // _CID: Compatible ID
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
            Name (_CRS, ResourceTemplate ()  // _CRS: Current Resource Settings
            {
                IRQNoFlags ()
                    {0,8,11}
                Memory32Fixed (ReadWrite,
                    0xFED00000,         // Address Base
                    0x00000400,         // Address Length
                    )
            })
        }
    }
}`, "[[name]]", name)
	} else {
		isMethod := hpet[0].Type == "Method"
		typ, method, mem := "BuffObj", "", "Default Base/Length - verify with your DSDT!"
		if isMethod {
			typ, method = "MethodObj", " ()"
		}
		if gotMem {
			mem = "Base/Length pulled from DSDT"
		}
		ssdtContent = `//
// Supplementary HPET _CRS from Goldfish64
// Requires the HPET's _CRS to XCRS rename
//
DefinitionBlock ("", "SSDT", 2, "ZPSS", "HPET", 0x00000000)
{
    External ([[name]], DeviceObj)
    External ([[name]].XCRS, [[type]])

    Scope ([[name]])
    {
        Name (BUFX, ResourceTemplate ()
        {
            IRQNoFlags ()
                {0,8,11}
            Memory32Fixed (ReadWrite,
                // [[mem]]
                [[mem_base]],         // Address Base
                [[mem_length]],         // Address Length
            )
        })
        Method (_CRS, 0, Serialized)  // _CRS: Current Resource Settings
        {
            // Return our buffer if booting macOS or the XCRS method
            // no longer exists for some reason
            If (LOr (_OSI ("Darwin"), LNot(CondRefOf ([[name]].XCRS))))
            {
                Return (BUFX)
            }
            // Not macOS and XCRS exists - return its result
            Return ([[name]].XCRS[[method]])
        }`
		ssdtContent = strings.ReplaceAll(ssdtContent, "[[name]]", name)
		ssdtContent = strings.ReplaceAll(ssdtContent, "[[type]]", typ)
		ssdtContent = strings.ReplaceAll(ssdtContent, "[[mem]]", mem)
		ssdtContent = strings.ReplaceAll(ssdtContent, "[[mem_base]]", memBase)
		ssdtContent = strings.ReplaceAll(ssdtContent, "[[mem_length]]", memLength)
		ssdtContent = strings.ReplaceAll(ssdtContent, "[[method]]", method)
		if hpetSTA {
			// Inject our external reference to the renamed XSTA method
			var ssdtParts []string
			external := false
			for _, line := range strings.Split(ssdtContent, "\n") {
				if strings.Contains(line, "External (") {
					external = true
				} else if external {
					ssdtParts = append(ssdtParts, "    External ("+name+".XSTA, "+sta.StaType+")")
					external = false
				}
				ssdtParts = append(ssdtParts, line)
			}
			ssdtContent = strings.Join(ssdtParts, "\n")
			// Add our method
			called := ""
			if sta.StaType == "MethodObj" {
				called = " ()"
			}
			ssdtContent += strings.ReplaceAll(strings.ReplaceAll(`
        Method (_STA, 0, NotSerialized)  // _STA: Status
        {
            // Return 0x0F if booting macOS or the XSTA method
            // no longer exists for some reason
            If (LOr (_OSI ("Darwin"), LNot (CondRefOf ([[name]].XSTA))))
            {
                Return (0x0F)
            }
            // Not macOS and XSTA exists - return its result
            Return ([[name]].XSTA[[called]])
        }`, "[[name]]", name), "[[called]]", called)
		}
		ssdtContent += `
    }
}`
	}

	enabled, err := g.WriteSSDT(ssdtName, ssdtContent, true)
	if err != nil {
		return nil, err
	}
	d := aAddDict(ssdtName, enabled)
	if patches == nil {
		patches = []any{}
	}
	d.Set("Patch", patches)
	return d, nil
}

// FixSystemClockAWAC mirrors fix_system_clock_awac. A nil dict is Python's None.
func (g *Guru) FixSystemClockAWAC() (*plist.Dict, error) {
	rtcRangeNeeded := false
	rtcCRSType := ""
	var crsLines []string
	lpcName := ""
	haveLPC := false
	awac, err := g.GetStaVar("STAS", "", "ACPI000E", "AWAC", false, nil)
	if err != nil {
		return nil, err
	}
	rtc, err := g.GetStaVar("STAS", "", "PNP0B00", "RTC", false, nil)
	if err != nil {
		return nil, err
	}
	rtcCRS := false
	// At this point - we should have any info about our AWAC and RTC devices
	// we need.  Let's see if we need an RTC fake - then build the SSDT.
	if !rtc.Valid {
		lpcName, haveLPC = g.GetLPCName(false, false, false)
		if !haveLPC {
			return nil, nil
		}
	} else {
		// Let's check if our RTC device has a _CRS variable - and if so, let's look for any skipped ranges
		rc := g.Acpi.GetMethodPaths(rtc.Device.Path+"._CRS", nil)
		if len(rc) == 0 {
			rc = g.Acpi.GetNamePaths(rtc.Device.Path+"._CRS", nil)
		}
		if len(rc) > 0 {
			rtcCRSType = "BuffObj"
			if rc[0].Type == "Method" {
				rtcCRSType = "MethodObj"
			}
			// Only check for the range if it's a buffobj
			if strings.ToLower(rtcCRSType) == "buffobj" {
				var lastAdr, lastLen int64
				haveLast := false
				crsScope := g.Acpi.GetScope(rc[0].Index, false, false, nil)
				// Let's try and clean up the scope - it's often a jumbled mess
				if len(crsScope) == 0 {
					return nil, fmt.Errorf("IndexError: list index out of range")
				}
				padLen := len(crsScope[0]) - len(strings.TrimLeftFunc(crsScope[0], pyIsSpace))
				pad := crsScope[0][:padLen]
				var fixedScope []string
				for _, line := range crsScope {
					if strings.HasPrefix(line, pad) { // Got a full line - strip the pad, and save it
						fixedScope = append(fixedScope, line[padLen:])
					} else { // Likely a part of the prior line
						if len(fixedScope) == 0 {
							return nil, fmt.Errorf("IndexError: list index out of range")
						}
						fixedScope[len(fixedScope)-1] += line
					}
				}
			scan:
				for i, line := range fixedScope {
					if strings.Contains(line, "Name (_CRS, ") {
						// Rename _CRS to BUFX for later - and strip any comments to avoid confusion
						line = strings.Split(strings.ReplaceAll(line, "Name (_CRS, ", "Name (BUFX, "), "  //")[0]
					}
					if strings.Contains(line, "IO (Decode16,") {
						// We have our start - get the the next line, and 4th line
						if i+4 >= len(fixedScope) {
							rtcRangeNeeded = false
							break scan
						}
						currAdr, ok1 := pyIntBase16(strings.Split(pyStrip(fixedScope[i+1]), ",")[0])
						currLen, ok2 := pyIntBase16(strings.Split(pyStrip(fixedScope[i+4]), ",")[0])
						if !ok1 || !ok2 { // Bad values? Bail...
							rtcRangeNeeded = false
							break scan
						}
						if haveLast { // Compare our range values
							adjust := currAdr - (lastAdr + lastLen)
							if adjust != 0 { // We need to increment the previous length by our adjust value
								// The original calls self.hexy, which does not exist;
								// the AttributeError is swallowed by a bare except.
								rtcRangeNeeded = false
								break scan
							}
						}
						// Save our last values
						lastAdr, lastLen, haveLast = currAdr, currLen, true
					}
					crsLines = append(crsLines, line)
				}
			}
			if rtcRangeNeeded { // We need to generate a rename for _CRS -> XCRS
				_, crsIndex, _ := g.Acpi.FindNextHex(rc[0].Index, nil)
				const crsHex, xcrsHex = "5F435253", "58435253"
				pad, err := g.Acpi.GetShortestUniquePad(crsHex, crsIndex, 0, nil)
				if err != nil {
					return nil, err
				}
				if pad == nil {
					return nil, fmt.Errorf("TypeError: cannot unpack non-iterable NoneType object")
				}
				rtc.Patches = append(rtc.Patches, STAPatch{
					Comment: rtc.DevName + " _CRS to XCRS Rename",
					Find:    pad.Left + crsHex + pad.Right,
					Replace: pad.Left + xcrsHex + pad.Right,
				})
				rtcCRS = true
			}
		}
	}
	// Let's see if we even need an SSDT
	// Not required if AWAC is not present; RTC is present, doesn't have an STAS var, and doesn't have an _STA method, and no range fixes are needed
	if !awac.Valid && rtc.Valid && !rtc.HasVar && len(rtc.Sta) == 0 && !rtcRangeNeeded {
		return nil, nil
	}
	// At this point - we need to do the following:
	// 1. Change STAS if needed
	// 2. Setup _STA with _OSI and call XSTA if needed
	// 3. Fake RTC if needed
	_ = rtcCRS // only feeds the unused "suffix" comment text in the original
	ssdtName := "SSDT-RTCAWAC"
	ssdt := `//
// Original sources from Acidanthera:
//  - https://github.com/acidanthera/OpenCorePkg/blob/master/Docs/AcpiSamples/SSDT-AWAC.dsl
//  - https://github.com/acidanthera/OpenCorePkg/blob/master/Docs/AcpiSamples/SSDT-RTC0.dsl
//
// Uses the ZPSS name to denote where this was created for troubleshooting purposes.
//
DefinitionBlock ("", "SSDT", 2, "ZPSS", "RTCAWAC", 0x00000000)
{
`
	if awac.HasVar || rtc.HasVar {
		ssdt += `    External (STAS, IntObj)
    Scope (\)
    {
        Method (_INI, 0, NotSerialized)  // _INI: Initialize
        {
            If (_OSI ("Darwin"))
            {
                Store (One, STAS)
            }
        }
    }
`
	}
	for _, x := range []*STAVar{awac, rtc} {
		if !x.Valid || x.HasVar || x.Device.Path == "" {
			continue
		}
		// Device was found, and it doesn't have the STAS var - check if we
		// have an _STA (which would be renamed)
		macos, original := "0x0F", "Zero"
		if x.DevHID == "ACPI000E" {
			macos, original = "Zero", "0x0F"
		}
		if len(x.Sta) > 0 {
			called := ""
			if x.StaType == "MethodObj" {
				called = " ()"
			}
			s := `    External ([[DevPath]], DeviceObj)
    External ([[DevPath]].XSTA, [[sta_type]])
    Scope ([[DevPath]])
    {
        Name (ZSTA, [[Original]])
        Method (_STA, 0, NotSerialized)  // _STA: Status
        {
            If (_OSI ("Darwin"))
            {
                Return ([[macOS]])
            }
            // Default to [[Original]] - but return the result of the renamed XSTA if possible
            If (CondRefOf ([[DevPath]].XSTA))
            {
                Store ([[DevPath]].XSTA[[called]], ZSTA)
            }
            Return (ZSTA)
        }
    }
`
			s = strings.ReplaceAll(s, "[[DevPath]]", x.Device.Path)
			s = strings.ReplaceAll(s, "[[Original]]", original)
			s = strings.ReplaceAll(s, "[[macOS]]", macos)
			s = strings.ReplaceAll(s, "[[sta_type]]", x.StaType)
			s = strings.ReplaceAll(s, "[[called]]", called)
			ssdt += s
		} else if x.DevHID == "ACPI000E" {
			// AWAC device with no STAS, and no _STA - let's just add one
			ssdt += strings.ReplaceAll(`    External ([[DevPath]], DeviceObj)
    Scope ([[DevPath]])
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
`, "[[DevPath]]", x.Device.Path)
		}
	}
	// Check if we need to setup an RTC range correction
	if rtcRangeNeeded && strings.ToLower(rtcCRSType) == "buffobj" && len(crsLines) > 0 && rtc.Valid {
		method := ""
		if rtcCRSType == "Method" {
			method = " ()"
		}
		newCRS := make([]string, len(crsLines))
		for i, x := range crsLines {
			newCRS[i] = strings.Repeat(" ", 8) + x
		}
		s := `    External ([[DevPath]], DeviceObj)
    External ([[DevPath]].XCRS, [[type]])
    Scope ([[DevPath]])
    {
        // Adjusted and renamed _CRS buffer ripped from DSDT with corrected range
[[NewCRS]]
        // End of adjusted _CRS and renamed buffer

        // Create a new _CRS method that returns the result of the renamed XCRS
        Method (_CRS, 0, Serialized)  // _CRS: Current Resource Settings
        {
            If (LOr (_OSI ("Darwin"), LNot (CondRefOf ([[DevPath]].XCRS))))
            {
                // Return our buffer if booting macOS or the XCRS method
                // no longer exists for some reason
                Return (BUFX)
            }
            // Not macOS and XCRS exists - return its result
            Return ([[DevPath]].XCRS[[method]])
        }
    }
`
		s = strings.ReplaceAll(s, "[[DevPath]]", rtc.Device.Path)
		s = strings.ReplaceAll(s, "[[type]]", rtcCRSType)
		s = strings.ReplaceAll(s, "[[method]]", method)
		s = strings.ReplaceAll(s, "[[NewCRS]]", strings.Join(newCRS, "\n"))
		ssdt += s
	}
	// Check if we do not have an RTC device at all
	if !rtc.Valid && lpcName != "" {
		ssdt += strings.ReplaceAll(`    External ([[LPCName]], DeviceObj)    // (from opcode)
    Scope ([[LPCName]])
    {
        Device (RTC0)
        {
            Name (_HID, EisaId ("PNP0B00"))  // _HID: Hardware ID
            Name (_CRS, ResourceTemplate ()  // _CRS: Current Resource Settings
            {
                IO (Decode16,
                    0x0070,             // Range Minimum
                    0x0070,             // Range Maximum
                    0x01,               // Alignment
                    0x08,               // Length
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
                    Return (0)
                }
            }
        }
    }
`, "[[LPCName]]", lpcName)
	}
	ssdt += "}"

	ok, err := g.WriteSSDT(ssdtName, ssdt, true)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	enabled, err := g.WriteSSDT(ssdtName, ssdt, true)
	if err != nil {
		return nil, err
	}
	d := aAddDict(ssdtName, enabled)
	patches := []any{}
	for _, p := range awac.Patches {
		patches = append(patches, aPatchDict(p))
	}
	for _, p := range rtc.Patches {
		patches = append(patches, aPatchDict(p))
	}
	d.Set("Patch", patches)
	return d, nil
}
