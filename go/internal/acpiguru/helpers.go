package acpiguru

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/dsdt"
)

// STAPatch is one {"Comment", "Find", "Replace"} entry of get_sta_var's "patches".
type STAPatch struct {
	Comment string
	Find    string
	Replace string
}

// STAVar mirrors the dict returned by get_sta_var. Only the fields the original
// sets for a given outcome are meaningful:
//   - device given but not found: Value=false (Valid=false)
//   - no device with the HID: Valid=false
//   - _STA already renamed to XSTA: Valid=false, Break=true, Device/DevName/DevHid/StaType set
//   - otherwise Valid=true with everything set.
type STAVar struct {
	Value   bool // the "value" key (always false when present)
	Valid   bool
	Break   bool
	HasVar  bool
	Sta     []dsdt.Path
	Patches []STAPatch
	Device  dsdt.Path
	DevName string
	DevHID  string
	Root    string
	StaType string
}

// GetStaVar mirrors get_sta_var. device == "" is Python's None.
func (g *Guru) GetStaVar(variable, device, devHID, devName string, logLocate bool, table *dsdt.Table) (*STAVar, error) {
	var devList []dsdt.Path
	if device != "" {
		devList = g.Acpi.GetDevicePaths(device, table)
		if len(devList) == 0 {
			if logLocate {
				g.logf(" - Could not locate %s\n", device)
			}
			return &STAVar{Value: false}, nil
		}
	} else {
		if logLocate {
			g.logf("Locating %s (%s) devices...\n", devHID, devName)
		}
		devList = g.Acpi.GetDevicePathsWithHID(devHID, table)
		if len(devList) == 0 {
			if logLocate {
				g.logf(" - Could not locate any %s devices\n", devHID)
			}
			return &STAVar{Valid: false}, nil
		}
	}
	dev := devList[0]
	if logLocate {
		g.logf(" - Found %s\n", dev.Path)
	}
	root := strings.Split(dev.Path, ".")[0]
	staType := "MethodObj"
	sta := g.Acpi.GetMethodPaths(dev.Path+"._STA", table)
	xsta := g.Acpi.GetMethodPaths(dev.Path+".XSTA", table)
	if len(sta) == 0 && len(xsta) == 0 {
		staType = "IntObj"
		sta = g.Acpi.GetNamePaths(dev.Path+"._STA", table)
		xsta = g.Acpi.GetNamePaths(dev.Path+".XSTA", table)
	}
	if len(xsta) > 0 && len(sta) == 0 {
		return &STAVar{Valid: false, Break: true, Device: dev, DevName: devName, DevHID: devHID, StaType: staType}, nil
	}
	hasVar := false
	if len(sta) > 0 && variable != "" {
		scope := strings.Join(g.Acpi.GetScope(sta[0].Index, false, true, table), "\n")
		hasVar = strings.Contains(scope, variable)
	}
	var patches []STAPatch
	if len(sta) > 0 && !hasVar {
		_, staIndex, _ := g.Acpi.FindNextHex(sta[0].Index, table)
		const staHex, xstaHex = "5F535441", "58535441" // _STA, XSTA
		pad, err := g.Acpi.GetShortestUniquePad(staHex, staIndex, 0, table)
		if err != nil {
			return nil, err
		}
		if pad == nil {
			return nil, fmt.Errorf("TypeError: cannot unpack non-iterable NoneType object")
		}
		patches = append(patches, STAPatch{
			Comment: devName + " _STA to XSTA Rename",
			Find:    pad.Left + staHex + pad.Right,
			Replace: pad.Left + xstaHex + pad.Right,
		})
	}
	return &STAVar{Valid: true, HasVar: hasVar, Sta: sta, Patches: patches, Device: dev, DevName: devName, DevHID: devHID, Root: root, StaType: staType}, nil
}

// GetLPCName mirrors get_lpc_name. ok is false where the original returns None.
func (g *Guru) GetLPCName(log, skipEC, skipCommonNames bool) (string, bool) {
	if log {
		g.logf("Locating LPC(B)/SBRG...\n")
	}
	for _, tableName := range SortedNicely(g.Acpi.AcpiTables.Names()) {
		table, _ := g.Acpi.AcpiTables.Get(tableName)
		if !skipEC {
			if ec := g.Acpi.GetDevicePathsWithHID("PNP0C09", table); len(ec) > 0 {
				parts := strings.Split(ec[0].Path, ".")
				name := strings.Join(parts[:len(parts)-1], ".")
				if log {
					g.logf(" - Found %s in %s\n", name, tableName)
				}
				return name, true
			}
		}
		if !skipCommonNames {
			for _, x := range []string{"LPCB", "LPC0", "LPC", "SBRG", "PX40"} {
				if paths := g.Acpi.GetDevicePaths(x, table); len(paths) > 0 {
					if log {
						g.logf(" - Found %s in %s\n", paths[0].Path, tableName)
					}
					return paths[0].Path, true
				}
			}
		}
		for _, path := range g.Acpi.GetPathOfType("Name", "_ADR", table) {
			adr, ok := g.GetAddressFromLine(path.Index, "_ADR, ", table)
			if !ok || (adr != 0x001F0000 && adr != 0x00140003) {
				continue
			}
			name := path.Path[:len(path.Path)-5]
			hid := name + "._HID"
			has := false
			for _, x := range table.Paths {
				if x.Path == hid {
					has = true
					break
				}
			}
			if has {
				continue
			}
			if log {
				g.logf(" - Found %s in %s\n", name, tableName)
			}
			return name, true
		}
	}
	if log {
		g.logf(" - Could not locate LPC(B)! Aborting!\n\n")
	}
	return "", false
}

// GetAddressFromLine mirrors get_address_from_line; ok is false where the
// original catches an exception and returns None.
func (g *Guru) GetAddressFromLine(line int, splitBy string, table *dsdt.Table) (int64, bool) {
	if table == nil {
		table = g.Acpi.GetDSDTOrOnly()
	}
	if table == nil || line >= len(table.Lines) || line < -len(table.Lines) {
		return 0, false
	}
	if line < 0 {
		line += len(table.Lines)
	}
	parts := strings.Split(table.Lines[line], splitBy)
	if len(parts) < 2 {
		return 0, false
	}
	s := strings.Split(parts[1], ")")[0]
	s = strings.ReplaceAll(strings.ReplaceAll(s, "Zero", "0x0"), "One", "0x1")
	return pyIntBase16(s)
}

// pyIntBase16 is int(s, 16): optional whitespace and sign, an optional 0x
// prefix and single underscores between digits.
func pyIntBase16(s string) (int64, bool) {
	s = pyStrip(s)
	neg := false
	if strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-") {
		neg = s[0] == '-'
		s = s[1:]
	}
	if len(s) >= 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X') {
		s = s[2:]
		if strings.HasPrefix(s, "_") {
			s = s[1:]
		}
	}
	if s == "" || strings.HasPrefix(s, "_") || strings.HasSuffix(s, "_") || strings.Contains(s, "__") {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.ReplaceAll(s, "_", ""), 16, 64)
	if err != nil {
		return 0, false
	}
	if neg {
		n = -n
	}
	return n, true
}
