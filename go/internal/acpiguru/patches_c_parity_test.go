package acpiguru

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/dsdt"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/dsdt/dsdttest"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyjson"
)

// Parity of the group C patches (patches_c.go) against the original
// Scripts/acpi_guru.py, through testdata/pyref_patches_c.py.

type pcTable struct {
	raw []byte
	dsl string
}

// pcBuild builds a table from text lines; a line "@HEX" is a block of raw bytes
// that is written to the table and shown as hex lines of the listing.
func pcBuild(sig, oem, id string, lines []string) pcTable {
	var body []byte
	var sb strings.Builder
	sb.WriteString("/*\n * Intel ACPI Component Architecture\n * AML/ASL+ Disassembler version 20230628\n */\n")
	n := 0
	for _, l := range lines {
		if !strings.HasPrefix(l, "@") {
			sb.WriteString(l + "\n")
			continue
		}
		b, err := hex.DecodeString(l[1:])
		if err != nil {
			panic(err)
		}
		for i := 0; i < len(b); i += 16 {
			end := i + 16
			if end > len(b) {
				end = len(b)
			}
			var parts []string
			for _, x := range b[i:end] {
				parts = append(parts, fmt.Sprintf("%02X", x))
			}
			line := fmt.Sprintf("    %04X: %s", 0x24+len(body)+i, strings.Join(parts, " "))
			if n%3 == 2 {
				line += "  // ........"
			}
			n++
			sb.WriteString(line + "\n")
		}
		body = append(body, b...)
	}
	sb.WriteString("\nTable Header:\n    Signature        : \"" + sig + "\"\n")
	hdr := make([]byte, 36)
	copy(hdr[0:4], sig)
	l := 36 + len(body)
	hdr[4], hdr[5], hdr[6], hdr[7] = byte(l), byte(l>>8), byte(l>>16), byte(l>>24)
	hdr[8] = 2
	copy(hdr[10:16], oem)
	copy(hdr[16:24], id)
	copy(hdr[28:32], "INTL")
	return pcTable{raw: append(hdr, body...), dsl: sb.String()}
}

// pcFill is deterministic filler bytes that never contain the patterns of interest.
func pcFill(seed, n int) string {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteString(fmt.Sprintf("%02X", 0x80+((seed*31+i*17)%0x40)))
	}
	return sb.String()
}

// pcMethodSta is the AML of "Method (_STA, 0) { Return (0x0F) }" with a seed-specific neighbourhood.
func pcMethodSta(seed int) string {
	return "@" + pcFill(seed, 5) + "140A5F535441" + "00A40A0F" + pcFill(seed+7, 5)
}

func pcNameSta(seed int) string {
	return "@" + pcFill(seed, 5) + "085F5354410A0F" + pcFill(seed+9, 5)
}

type pcDS struct {
	lpc    string // "", "LPCB", "LPC0", "SBRG", "PX40", "XLPC" (ADR only), "XLPH" (ADR and _HID)
	ec     bool
	igpu   string // "", "IGPU", "GFX0"
	pnlf   bool
	pnlfTx bool // PNLF only as a comment
	nbcf   string
	gpio   string // "", "method", "name", "xsta", "xstaname", "both", "none", "gpio-only", "two"
	rtc    string // "", "RTC", "RTC0", "RTC_", "RTC1+RTC0"
	rtcSta string // "", "method", "name", "xsta"
	awac   bool
	hp     bool
	gprw   bool
	uprw   bool
	uswe   bool
	wole   bool
	unc0   bool
	osid   bool
	osif   bool
	osi    []string
	// odd bytes that look like but are not the wake patterns.
	fakeWake bool
	// dupSta adds other devices with _STA: 1 different neighbours, 2 same as the
	// GPI0/RTC ones, 3 several identical copies.
	dupSta int
}

func (d pcDS) lines() []string {
	var l []string
	add := func(s ...string) { l = append(l, s...) }
	add(`DefinitionBlock ("", "DSDT", 2, "ALASKA", "A M I ", 0x01072009)`, `{`,
		`    External (_SB_.PCI0.XHC_, DeviceObj)    // (from opcode)`)
	if d.pnlfTx {
		add(`    // PNLF is only mentioned in this comment`)
	}
	add(`    Scope (\_SB)`, `    {`, `        Device (PCI0)`, `        {`,
		`            Name (_HID, EisaId ("PNP0A08"))  // _HID: Hardware ID`,
		`            Name (_ADR, Zero)  // _ADR: Address`, "@"+pcFill(1, 20))
	if d.igpu != "" {
		add(`            Device (`+d.igpu+`)`, `            {`, `                Name (_ADR, 0x00020000)  // _ADR: Address`, `            }`)
	}
	if d.pnlf {
		add(`            Device (PNLF)`, `            {`,
			`                Name (_HID, EisaId ("APP0002"))  // _HID: Hardware ID`,
			`                Name (_CID, "backlight")  // _CID: Compatible ID`, `            }`, "@"+pcFill(2, 20))
	}
	if d.lpc != "" {
		name := d.lpc
		if name == "XLPC" || name == "XLPH" {
			name = "XLPC"
		}
		add(`            Device (`+name+`)`, `            {`, `                Name (_ADR, 0x001F0000)  // _ADR: Address`)
		if d.lpc == "XLPH" {
			add(`                Name (_HID, EisaId ("PNP0A05"))  // _HID: Hardware ID`)
		}
		if d.ec {
			add(`                Device (EC0_)`, `                {`, `                    Name (_HID, EisaId ("PNP0C09"))  // _HID: Hardware ID`, `                }`)
		}
		d.rtcBlock(&l, "                ")
		if d.gpio != "" {
			d.gpioBlock(&l, "                ")
		}
		add(`            }`)
	} else {
		if d.ec {
			add(`            Device (EC0_)`, `            {`, `                Name (_HID, EisaId ("PNP0C09"))  // _HID: Hardware ID`, `            }`)
		}
		d.rtcBlock(&l, "            ")
		if d.gpio != "" {
			d.gpioBlock(&l, "            ")
		}
	}
	add(`        }`)
	if d.awac {
		add(`        Device (AWAC)`, `        {`, `            Name (_HID, "ACPI000E")  // _HID: Hardware ID`, `        }`)
	}
	if d.unc0 {
		add(`        Device (UNC0)`, `        {`, `            Name (_HID, EisaId ("PNP0A03"))  // _HID: Hardware ID`,
			`            Name (_UID, 0x80)  // _UID: Unique ID`, `        }`, "@"+pcFill(3, 16))
	}
	add(`    }`)
	for k := 0; k < d.dupSta; k++ {
		seed := 90 + k
		if d.dupSta >= 2 {
			seed = 40
			if d.dupSta == 3 && k == 2 {
				seed = 30
			}
			if d.dupSta == 2 {
				seed = 30 + (k%2)*10
			}
		}
		add(`    Scope (\_SB)`, `    {`, fmt.Sprintf(`        Device (DEV%d)`, k), `        {`, `            Method (_STA, 0, NotSerialized)`, pcMethodSta(seed), `            {`, `                Return (0x0F)`, `            }`, `        }`, `    }`)
	}
	if d.osid {
		add(`    Method (OSID, 0, Serialized)`, `    {`, `        Return (0x07DC)`, `    }`, "@"+pcFill(4, 8)+"144C054F534944"+pcFill(5, 4))
	}
	if d.osif {
		add(`    Method (OSIF, 1, NotSerialized)`, `    {`, `        Return (Arg0)`, `    }`, "@"+pcFill(6, 8))
	}
	for i, s := range d.osi {
		add(fmt.Sprintf(`    Method (CHK%d, 0, NotSerialized)`, i), `    {`, `        If (_OSI ("`+s+`"))`, `        {`, `            Return (One)`, `        }`, `    }`)
	}
	if d.gprw {
		add(`    Method (GPRW, 2, NotSerialized)`, `    {`, `        Return (Package (0x02) { Zero, Zero })`, `    }`, "@"+pcFill(7, 6)+"1410475052570200"+pcFill(8, 6))
	}
	if d.uprw {
		add(`    Method (UPRW, 2, NotSerialized)`, `    {`, `        Return (Package (0x02) { Zero, Zero })`, `    }`, "@"+pcFill(9, 6)+"1410555052570200"+pcFill(10, 6))
	}
	if d.uswe {
		add(`    If ((USWE == One))`, `    {`, `    }`, "@"+pcFill(11, 4)+"9355535745"+pcFill(12, 4))
	}
	if d.wole {
		add(`    If ((WOLE == One))`, `    {`, `    }`, "@"+pcFill(13, 4)+"93574F4C45"+pcFill(14, 4))
	}
	if d.fakeWake {
		// GPRW/UPRW without the 02 argument count, USWE/WOLE without the 93 opcode.
		add(`    Method (GPRW, 1, NotSerialized)`, `    {`, `    }`, "@"+pcFill(15, 4)+"47505257014750525703555052570055535745"+"574F4C45"+pcFill(16, 4))
	}
	if d.hp {
		add(`    OperationRegion (RTCM, SystemIO, 0x70, 0x02)`, "@"+pcFill(17, 4)+"4701700070000108"+pcFill(18, 4))
	}
	switch d.nbcf {
	case "0A00":
		add(`    Name (NBCF, Zero)`, "@"+pcFill(19, 3)+"084E4243460A00"+pcFill(20, 3))
	case "00":
		add(`    Name (NBCF, Zero)`, "@"+pcFill(19, 3)+"084E42434600"+pcFill(20, 3))
	case "both":
		add(`    Name (NBCF, Zero)`, "@"+pcFill(19, 3)+"084E42434600"+pcFill(20, 3)+"084E4243460A00")
	}
	add(`}`)
	return l
}

func (d pcDS) rtcBlock(l *[]string, ind string) {
	if d.rtc == "" {
		return
	}
	names := []string{d.rtc}
	if d.rtc == "RTC1+RTC0" {
		names = []string{"RTC1", "RTC0"}
	}
	for k, name := range names {
		if d.rtc == "RTC1+RTC0" && k == 0 {
			// A decoy device that makes RTC1 taken; not the RTC itself.
			*l = append(*l, ind+`Device (RTC1)`, ind+`{`, ind+`    Name (_ADR, 0x1234)  // _ADR: Address`, ind+`}`)
			continue
		}
		*l = append(*l, ind+`Device (`+name+`)`, ind+`{`, ind+`    Name (_HID, EisaId ("PNP0B00"))  // _HID: Hardware ID`)
		switch d.rtcSta {
		case "method":
			*l = append(*l, ind+`    Method (_STA, 0, NotSerialized)  // _STA: Status`, pcMethodSta(30), ind+`    {`, ind+`        Return (0x0F)`, ind+`    }`)
		case "name":
			*l = append(*l, ind+`    Name (_STA, 0x0F)  // _STA: Status`, pcNameSta(31))
		case "xsta":
			*l = append(*l, ind+`    Method (XSTA, 0, NotSerialized)  // _STA: Status`, pcMethodSta(32), ind+`    {`, ind+`        Return (0x0F)`, ind+`    }`)
		}
		*l = append(*l, ind+`}`)
	}
}

func (d pcDS) gpioBlock(l *[]string, ind string) {
	name := "GPI0"
	if d.gpio == "gpio-only" {
		name = "GPIO"
	}
	for k := 0; k < 1+map[bool]int{true: 1}[d.gpio == "two"]; k++ {
		if k == 1 {
			name = "GPI0"
			*l = append(*l, ind+`Scope (\_SB)`, ind+`{`)
		}
		*l = append(*l, ind+`Device (`+name+`)`, ind+`{`, ind+`    Name (_HID, "INT3452")  // _HID: Hardware ID`)
		method := func(n string, seed int) {
			*l = append(*l, ind+`    Method (`+n+`, 0, NotSerialized)  // _STA: Status`, pcMethodSta(seed), ind+`    {`, ind+`        Return (0x0F)`, ind+`    }`)
		}
		switch d.gpio {
		case "method", "gpio-only", "two":
			method("_STA", 40+k)
		case "name":
			*l = append(*l, ind+`    Name (_STA, 0x0F)  // _STA: Status`, pcNameSta(41))
		case "xsta":
			method("XSTA", 42)
		case "xstaname":
			*l = append(*l, ind+`    Name (XSTA, 0x0F)  // _STA: Status`, pcNameSta(43))
		case "both":
			method("_STA", 44)
			method("XSTA", 45)
		}
		*l = append(*l, ind+`}`)
		if k == 1 {
			*l = append(*l, ind+`}`)
		}
	}
}

func (d pcDS) table() pcTable { return pcBuild("DSDT", "ALASKA", "A M I   ", d.lines()) }

func pcSSDT(extra ...string) pcTable {
	lines := []string{`DefinitionBlock ("", "SSDT", 2, "PmRef", "Cpu0Ist", 0x00003000)`, `{`,
		`    Scope (\_PR)`, `    {`, `        Processor (CPU0, 0x01, 0x00001810, 0x06)`, `        {`, `        }`, `    }`}
	lines = append(lines, extra...)
	lines = append(lines, `}`)
	return pcBuild("SSDT", "PmRef ", "Cpu0Ist ", lines)
}

type pcCase struct {
	name    string
	method  string
	tables  map[string]pcTable
	report  *string
	model   string
	mac     string
	compile string
	noLPC   bool
}

type pcResult struct {
	OK      any               `json:"ok"`
	Error   bool              `json:"error"`
	Message string            `json:"message"`
	Files   map[string]string `json:"files"`
	LPC     any               `json:"lpc"`
}

func pcStr(s string) *string { return &s }

func pcReport(gpus ...string) *string {
	return pcStr(`{"GPU": {` + strings.Join(gpus, ", ") + `}}`)
}

func pcGPU(name, codename, path string) string {
	var f []string
	if codename != "-" {
		f = append(f, `"Codename": "`+codename+`"`)
	}
	if path != "-" {
		f = append(f, `"ACPI Path": "`+path+`"`)
	}
	return `"` + name + `": {` + strings.Join(f, ", ") + `}`
}

func pcCases() []pcCase {
	var cs []pcCase
	add := func(c pcCase) { cs = append(cs, c) }
	one := func(d pcDS) map[string]pcTable { return map[string]pcTable{"DSDT.aml": d.table()} }
	compiles := []string{"ok", "fail", ""}

	// EnableBacklightControls
	codenames := []string{"Iron Lake", "Sandy Bridge", "Ivy Bridge", "Haswell", "Broadwell", "Skylake", "Kaby Lake", "Coffee Lake", "Ice Lake", "Tiger Lake", "", "-", "sandy bridge"}
	paths := []string{"_SB.PCI0.IGPU", "_SB.PCI0.GFX0", "", "-"}
	dsVariants := []pcDS{
		{lpc: "LPCB", igpu: "IGPU"},
		{lpc: "LPCB", igpu: "IGPU", pnlf: true},
		{lpc: "LPCB", igpu: "GFX0", pnlf: true, nbcf: "0A00"},
		{lpc: "LPCB", pnlfTx: true, nbcf: "00"},
		{lpc: "LPCB", pnlf: true, nbcf: "both"},
	}
	for ci, cn := range codenames {
		for pi, p := range paths {
			d := dsVariants[(ci+pi)%len(dsVariants)]
			add(pcCase{name: fmt.Sprintf("backlight/%s/%s", cn, p), method: "enable_backlight_controls", tables: one(d),
				report: pcReport(pcGPU("Intel HD", cn, p)), compile: compiles[(ci+pi)%3]})
		}
	}
	for vi, d := range dsVariants {
		for _, cn := range []string{"Ivy Bridge", "Skylake", "Coffee Lake"} {
			// dGPU listed first, iGPU last, and the reverse (the last GPU decides).
			add(pcCase{name: fmt.Sprintf("backlight/two-gpu-a/%d/%s", vi, cn), method: "enable_backlight_controls", tables: one(d),
				report: pcReport(pcGPU("dGPU", "Navi 10", "_SB.PCI0.PEG0.PEGP"), pcGPU("iGPU", cn, "_SB.PCI0.IGPU")), compile: "ok"})
			add(pcCase{name: fmt.Sprintf("backlight/two-gpu-b/%d/%s", vi, cn), method: "enable_backlight_controls", tables: one(d),
				report: pcReport(pcGPU("iGPU", cn, "_SB.PCI0.IGPU"), pcGPU("dGPU", "Navi 10", "_SB.PCI0.PEG0.PEGP")), compile: "ok"})
		}
	}
	// NBCF found in a later table, in the first table only, in both.
	for _, nb := range []string{"0A00", "00", "both", ""} {
		for _, sn := range []string{"0A00", "00", ""} {
			ss := pcSSDT()
			switch sn {
			case "0A00":
				ss = pcSSDT(`    Name (NBCF, Zero)`, "@"+pcFill(50, 3)+"084E4243460A00"+pcFill(51, 3))
			case "00":
				ss = pcSSDT(`    Name (NBCF, Zero)`, "@"+pcFill(50, 3)+"084E42434600"+pcFill(51, 3))
			}
			add(pcCase{name: "backlight/nbcf-multi/" + nb + "/" + sn, method: "enable_backlight_controls",
				tables: map[string]pcTable{"DSDT.aml": pcDS{lpc: "LPCB", igpu: "IGPU", pnlf: true, nbcf: nb}.table(), "SSDT-1.aml": ss, "SSDT-10.aml": pcSSDT(`    Name (NBCF, Zero)`, "@"+pcFill(52, 3)+"084E42434600")},
				report: pcReport(pcGPU("Intel HD", "Haswell", "_SB.PCI0.IGPU")), compile: "ok"})
		}
	}
	// SSDT naming that changes the sort order (SSDT-2 before SSDT-10 numerically, "A" tables).
	add(pcCase{name: "backlight/nbcf-sort", method: "enable_backlight_controls",
		tables: map[string]pcTable{"DSDT.aml": pcDS{lpc: "LPCB"}.table(),
			"SSDT-10.aml": pcSSDT(`    Name (NBCF, Zero)`, "@084E42434600"), "SSDT-2.aml": pcSSDT(`    Name (NBCF, Zero)`, "@084E4243460A00")},
		report: pcReport(pcGPU("Intel HD", "Skylake", "_SB.PCI0.IGPU")), compile: "ok"})
	// Report problems.
	add(pcCase{name: "backlight/no-report", method: "enable_backlight_controls", tables: one(pcDS{lpc: "LPCB"}), report: nil})
	add(pcCase{name: "backlight/no-gpu-key", method: "enable_backlight_controls", tables: one(pcDS{lpc: "LPCB"}), report: pcStr(`{}`)})
	add(pcCase{name: "backlight/empty-gpu", method: "enable_backlight_controls", tables: one(pcDS{lpc: "LPCB"}), report: pcStr(`{"GPU": {}}`)})
	add(pcCase{name: "backlight/gpu-null-path", method: "enable_backlight_controls", tables: one(pcDS{lpc: "LPCB"}), report: pcStr(`{"GPU": {"a": {"Codename": "Ivy Bridge", "ACPI Path": null}}}`)})
	add(pcCase{name: "backlight/gpu-null-codename", method: "enable_backlight_controls", tables: one(pcDS{lpc: "LPCB"}), report: pcStr(`{"GPU": {"a": {"Codename": null, "ACPI Path": "_SB.X"}}}`)})
	add(pcCase{name: "backlight/gpu-int-path", method: "enable_backlight_controls", tables: one(pcDS{lpc: "LPCB"}), report: pcStr(`{"GPU": {"a": {"Codename": "Haswell", "ACPI Path": 5}}}`)})
	add(pcCase{name: "backlight/gpu-list", method: "enable_backlight_controls", tables: one(pcDS{lpc: "LPCB"}), report: pcStr(`{"GPU": [1,2]}`)})
	add(pcCase{name: "backlight/gpu-not-dict", method: "enable_backlight_controls", tables: one(pcDS{lpc: "LPCB"}), report: pcStr(`{"GPU": {"a": 3}}`)})

	// EnableGPIODevice
	for _, g := range []string{"", "method", "name", "xsta", "xstaname", "both", "none", "gpio-only", "two"} {
		for _, lpc := range []string{"LPCB", ""} {
			for _, comp := range []string{"ok", "fail"} {
				add(pcCase{name: fmt.Sprintf("gpio/%s/%s/%s", g, lpc, comp), method: "enable_gpio_device",
					tables: one(pcDS{lpc: lpc, ec: true, gpio: g}), compile: comp})
			}
		}
	}
	for dup := 1; dup <= 3; dup++ {
		for _, g := range []string{"method", "name", "both"} {
			add(pcCase{name: fmt.Sprintf("gpio/dup%d/%s", dup, g), method: "enable_gpio_device", compile: "ok", tables: one(pcDS{lpc: "LPCB", gpio: g, dupSta: dup})})
		}
		for _, sta := range []string{"method", "name"} {
			add(pcCase{name: fmt.Sprintf("hedt/dup%d/%s", dup, sta), method: "fix_system_clock_hedt", compile: "ok", tables: one(pcDS{lpc: "LPCB", rtc: "RTC", rtcSta: sta, dupSta: dup})})
		}
	}
	// _STA without hex data after it: the pad lookup raises.
	add(pcCase{name: "gpio/no-hex", method: "enable_gpio_device", compile: "ok", tables: map[string]pcTable{"DSDT.aml": pcBuild("DSDT", "ALASKA", "A M I   ", []string{
		`DefinitionBlock ("", "DSDT", 2, "ALASKA", "A M I ", 0x01072009)`, `{`, `    Scope (\_SB)`, `    {`, `        Device (GPI0)`, `        {`,
		`            Name (_HID, "INT3452")`, `            Method (_STA, 0, NotSerialized)`, `            {`, `                Return (0x0F)`, `            }`, `        }`, `    }`, `}`, "@" + pcFill(1, 16)})}})
	add(pcCase{name: "gpio/gpi0-other-table", method: "enable_gpio_device", compile: "ok", tables: map[string]pcTable{"DSDT.aml": pcDS{lpc: "LPCB"}.table(),
		"SSDT-1.aml": pcSSDT(`    Scope (\_SB)`, `    {`, `        Device (GPI0)`, `        {`, `            Name (_HID, "INT3452")`, `        }`, `    }`)}})

	// EnableNVRAMSupport
	for _, lpc := range []string{"", "LPCB", "LPC0", "SBRG", "PX40", "XLPC", "XLPH"} {
		for _, ec := range []bool{false, true} {
			for _, noLPC := range []bool{false, true} {
				add(pcCase{name: fmt.Sprintf("nvram/%s/%v/%v", lpc, ec, noLPC), method: "enable_nvram_support", tables: one(pcDS{lpc: lpc, ec: ec}), noLPC: noLPC, compile: compiles[len(lpc)%3]})
			}
		}
	}
	add(pcCase{name: "nvram/lpc-in-ssdt", method: "enable_nvram_support", compile: "ok", tables: map[string]pcTable{"DSDT.aml": pcDS{}.table(), "SSDT-1.aml": pcSSDT(`    Scope (\_SB.PCI0)`, `    {`, `        Device (LPCB)`, `        {`, `            Name (_ADR, 0x001F0000)`, `        }`, `    }`)}})

	// RemoveConditionalScope
	add(pcCase{name: "scope/a", method: "remove_conditional_scope", tables: one(pcDS{lpc: "LPCB"})})
	add(pcCase{name: "scope/b", method: "remove_conditional_scope", tables: one(pcDS{}), noLPC: true})

	// FixHP005PostError
	for _, hp := range []bool{false, true} {
		for _, other := range []bool{false, true} {
			tbl := map[string]pcTable{"DSDT.aml": pcDS{lpc: "LPCB", hp: hp}.table()}
			if other {
				tbl["SSDT-1.aml"] = pcSSDT("@" + pcFill(60, 3) + "4701700070000108")
			}
			add(pcCase{name: fmt.Sprintf("hp005/%v/%v", hp, other), method: "fix_hp_005_post_error", tables: tbl})
		}
	}

	// AddNullEthernetDevice
	for i, mac := range []string{"AABBCCDDEEFF", "0123456789AB", "00", "ABC", "", "A", "0011223344556677", "aabbccddeeff"} {
		for _, comp := range compiles {
			add(pcCase{name: fmt.Sprintf("eth/%d/%s", i, comp), method: "add_null_ethernet_device", tables: one(pcDS{lpc: "LPCB"}), mac: mac, compile: comp})
		}
	}

	// FixSystemClockHEDT
	for _, rtc := range []string{"", "RTC", "RTC0", "RTC_", "RTC1+RTC0"} {
		for _, sta := range []string{"", "method", "name", "xsta"} {
			for _, awac := range []bool{false, true} {
				for _, lpc := range []string{"LPCB", ""} {
					add(pcCase{name: fmt.Sprintf("hedt/%s/%s/%v/%s", rtc, sta, awac, lpc), method: "fix_system_clock_hedt",
						tables: one(pcDS{lpc: lpc, ec: lpc != "", rtc: rtc, rtcSta: sta, awac: awac}), compile: "ok"})
				}
			}
		}
	}
	// No RTC HID device, LPC found, with and without an existing RTC0 device (other HID).
	add(pcCase{name: "hedt/lpc-rtc0-existing", method: "fix_system_clock_hedt", compile: "ok", tables: map[string]pcTable{"DSDT.aml": pcDS{lpc: "LPCB"}.table(),
		"SSDT-1.aml": pcSSDT(`    Scope (\_SB.PCI0.LPCB)`, `    {`, `        Device (RTC0)`, `        {`, `            Name (_HID, "PNP0B01")`, `            Method (_STA, 0, NotSerialized)`, "@"+pcMethodSta(70)[1:], `            {`, `                Return (0x0F)`, `            }`, `        }`, `    }`)}})
	add(pcCase{name: "hedt/rtc-no-hex", method: "fix_system_clock_hedt", compile: "ok", tables: map[string]pcTable{"DSDT.aml": pcBuild("DSDT", "ALASKA", "A M I   ", []string{
		`DefinitionBlock ("", "DSDT", 2, "ALASKA", "A M I ", 0x01072009)`, `{`, `    Scope (\_SB.PCI0.LPCB)`, `    {`, `        Device (RTC)`, `        {`,
		`            Name (_HID, EisaId ("PNP0B00"))`, `            Method (_STA, 0, NotSerialized)`, `            {`, `                Return (0x0F)`, `            }`, `        }`, `    }`, `}`, "@" + pcFill(2, 16)})}})
	add(pcCase{name: "hedt/two-awac", method: "fix_system_clock_hedt", compile: "fail", tables: one(pcDS{lpc: "LPCB", rtc: "RTC", rtcSta: "method", awac: true})})

	// InstantWakeFix
	for m := 0; m < 16; m++ {
		d := pcDS{lpc: "LPCB", gprw: m&1 != 0, uprw: m&2 != 0, uswe: m&4 != 0, wole: m&8 != 0}
		for _, comp := range compiles {
			add(pcCase{name: fmt.Sprintf("prw/%04b/%s", m, comp), method: "instant_wake_fix", tables: one(d), compile: comp})
		}
	}
	add(pcCase{name: "prw/fake-bytes-only", method: "instant_wake_fix", compile: "ok", tables: one(pcDS{lpc: "LPCB", fakeWake: true})})
	add(pcCase{name: "prw/fake-and-real", method: "instant_wake_fix", compile: "ok", tables: one(pcDS{lpc: "LPCB", fakeWake: true, uprw: true, wole: true})})
	add(pcCase{name: "prw/only-in-ssdt", method: "instant_wake_fix", compile: "ok", tables: map[string]pcTable{"DSDT.aml": pcDS{lpc: "LPCB"}.table(),
		"SSDT-1.aml": pcSSDT("@4750525702" + "5555535745")}})

	// FixUncoreBridge
	for _, unc := range []bool{false, true} {
		for _, comp := range compiles {
			add(pcCase{name: fmt.Sprintf("unc/%v/%s", unc, comp), method: "fix_uncore_bridge", tables: one(pcDS{lpc: "LPCB", unc0: unc}), compile: comp})
		}
	}
	add(pcCase{name: "unc/in-ssdt-only", method: "fix_uncore_bridge", compile: "ok", tables: map[string]pcTable{"DSDT.aml": pcDS{lpc: "LPCB"}.table(),
		"SSDT-1.aml": pcSSDT(`    Scope (\_SB)`, `    {`, `        Device (UNC0)`, `        {`, `        }`, `    }`)}})

	// OperatingSystemPatch
	osiSets := [][]string{
		nil,
		{"Windows 2015"},
		{"Windows 2001"},
		{"Windows 2001.1"},
		{"Windows 2009", "Windows 2012", "Windows 2013", "Windows 2015"},
		{"Windows 2000", "Windows 2001 SP1", "Windows 2001.1 SP1", "Windows 2001 SP2", "Windows 2006", "Windows 2006 SP1", "Windows 2006.1"},
		{"Windows 2016", "Windows 2017", "Windows 2017.2", "Windows 2018", "Windows 2018.2", "Windows 2019", "Windows 2020", "Windows 2021", "Windows 2022"},
		{"Linux", "Darwin", "Windows 95"},
		{"Windows 2022"},
	}
	for i, set := range osiSets {
		for _, osid := range []bool{false, true} {
			for _, osif := range []bool{false, true} {
				add(pcCase{name: fmt.Sprintf("osi/%d/%v/%v", i, osid, osif), method: "operating_system_patch",
					tables: one(pcDS{lpc: "LPCB", osi: set, osid: osid, osif: osif}), compile: compiles[(i+len(set))%3]})
			}
		}
	}
	add(pcCase{name: "osi/other-table", method: "operating_system_patch", compile: "ok", tables: map[string]pcTable{"DSDT.aml": pcDS{lpc: "LPCB"}.table(),
		"SSDT-1.aml": pcSSDT(`    Method (OSID, 0, NotSerialized)`, `    {`, `        If (_OSI ("Windows 2015"))`, `        {`, `        }`, `    }`)}})

	// smbios model is carried through (unused by group C) with laptops, desktops, HEDT codenames.
	models := []string{"MacBookPro16,1", "iMac20,1", "MacPro7,1", "iMacPro1,1", "MacBookAir9,1", "MacMini8,1", ""}
	meths := []string{"enable_backlight_controls", "enable_gpio_device", "enable_nvram_support", "fix_system_clock_hedt", "instant_wake_fix", "operating_system_patch", "fix_uncore_bridge", "add_null_ethernet_device"}
	for i, m := range models {
		for j, me := range meths {
			d := pcDS{lpc: "LPCB", ec: i%2 == 0, igpu: "IGPU", pnlf: j%2 == 0, gpio: "method", rtc: "RTC", rtcSta: "method", awac: i%3 == 0, gprw: true, uswe: i%2 == 1, unc0: i == 2, osi: []string{"Windows 2015", "Windows 2013"}}
			add(pcCase{name: fmt.Sprintf("models/%s/%s", m, me), method: me, tables: one(d), model: m, mac: "112233445566", compile: "ok",
				report: pcReport(pcGPU("iGPU", codenames[(i+j)%8], "_SB.PCI0.IGPU"))})
		}
	}
	return cs
}

func pcRunOracle(t *testing.T, cases []map[string]any, iasl string) []pcResult {
	t.Helper()
	in, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("python3", "-I", "testdata/pyref_patches_c.py", repoRoot(t), iasl)
	cmd.Stdin = bytes.NewReader(in)
	cmd.Env = os.Environ()
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("oracle failed: %v\n%s", err, errb.String())
	}
	var res []pcResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("oracle output: %v\n%s", err, out.String())
	}
	return res
}

func pcCall(g *Guru, method string) (*plist.Dict, error) {
	switch method {
	case "enable_backlight_controls":
		return g.EnableBacklightControls()
	case "enable_gpio_device":
		return g.EnableGPIODevice()
	case "enable_nvram_support":
		return g.EnableNVRAMSupport()
	case "remove_conditional_scope":
		return g.RemoveConditionalScope()
	case "fix_hp_005_post_error":
		return g.FixHP005PostError()
	case "add_null_ethernet_device":
		return g.AddNullEthernetDevice()
	case "fix_system_clock_hedt":
		return g.FixSystemClockHEDT()
	case "instant_wake_fix":
		return g.InstantWakeFix()
	case "fix_uncore_bridge":
		return g.FixUncoreBridge()
	case "operating_system_patch":
		return g.OperatingSystemPatch()
	}
	panic("unknown method " + method)
}

func TestParityPatchesC(t *testing.T) {
	cases := pcCases()
	base := t.TempDir()
	iasl := filepath.Join(base, "iasl")
	if err := os.WriteFile(iasl, []byte(dsdttest.FakeIasl), 0o755); err != nil {
		t.Fatal(err)
	}
	var oracle []map[string]any
	for i, c := range cases {
		dir := filepath.Join(base, fmt.Sprint(i), "tables")
		dslDir := filepath.Join(base, fmt.Sprint(i), "dsl")
		for _, d := range []string{dir, dslDir} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		for name, tb := range c.tables {
			if err := os.WriteFile(filepath.Join(dir, name), tb.raw, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dslDir, name+".dsl"), []byte(tb.dsl), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		m := map[string]any{"method": c.method, "dir": dir, "dsl": dslDir, "out": filepath.Join(base, fmt.Sprint(i), "out-py"),
			"model": c.model, "compile": c.compile, "lpc": !c.noLPC}
		if c.mac != "" || strings.HasPrefix(c.name, "eth/") {
			m["mac"] = c.mac
		}
		if c.report != nil {
			m["report"] = *c.report
		}
		oracle = append(oracle, m)
	}
	py := pcRunOracle(t, oracle, iasl)
	if len(py) != len(cases) {
		t.Fatalf("oracle returned %d results for %d cases", len(py), len(cases))
	}

	stats := map[string]int{}
	for i, c := range cases {
		want := py[i]
		dir := oracle[i]["dir"].(string)
		out := filepath.Join(base, fmt.Sprint(i), "out-go")
		os.Setenv("FAKE_DSL_DIR", oracle[i]["dsl"].(string))
		os.Unsetenv("FAKE_COMPILE")
		g := New(dsdt.New(iasl), func(string) (string, error) { return "", io.EOF })
		g.ReadACPITables(dir)
		g.Dsdt = g.Acpi.GetDSDTOrOnly()
		if c.report != nil {
			rep, err := pyjson.Load([]byte(*c.report))
			if err != nil {
				t.Fatal(err)
			}
			g.HardwareReport = rep
		}
		g.ACPIDirectory = out
		g.SMBIOSModel = c.model
		if !c.noLPC {
			g.LPCBusDevice, _ = g.GetLPCName(false, false, false)
		}
		mac := "AABBCCDDEEFF"
		if v, ok := oracle[i]["mac"].(string); ok {
			mac = v
		}
		g.RandomMAC = func() string { return mac }
		if c.compile != "" {
			os.Setenv("FAKE_COMPILE", c.compile)
		}
		res, err := pcCall(g, c.method)
		os.Unsetenv("FAKE_COMPILE")

		files := map[string]string{}
		if ents, rerr := os.ReadDir(out); rerr == nil {
			for _, e := range ents {
				b, _ := os.ReadFile(filepath.Join(out, e.Name()))
				files[e.Name()] = hex.EncodeToString(b)
			}
		}
		if (err != nil) != want.Error {
			t.Errorf("%s: Go error=%v (%v), Python error=%v (%s)", c.name, err != nil, err, want.Error, want.Message)
			continue
		}
		if g.LPCBusDevice != "" && want.LPC != g.LPCBusDevice || g.LPCBusDevice == "" && want.LPC != nil {
			t.Errorf("%s: lpc Go %q, Python %v", c.name, g.LPCBusDevice, want.LPC)
		}
		var got any
		if res != nil {
			got = normalize(res)
		}
		if !reflect.DeepEqual(got, normalizeAny(want.OK)) {
			gj, _ := json.Marshal(got)
			wj, _ := json.Marshal(want.OK)
			t.Errorf("%s: result differs\n Go:     %s\n Python: %s", c.name, pcTrim(gj), pcTrim(wj))
		}
		wf := want.Files
		if wf == nil {
			wf = map[string]string{}
		}
		if !reflect.DeepEqual(files, wf) {
			for name := range pcUnion(files, wf) {
				if files[name] != wf[name] {
					gb, _ := hex.DecodeString(files[name])
					wb, _ := hex.DecodeString(wf[name])
					t.Errorf("%s: file %s differs (Go has=%v, Python has=%v)\n%s", c.name, name, files[name] != "", wf[name] != "", pcDiff(string(gb), string(wb)))
				}
			}
		}
		stats[c.method]++
		if want.Error {
			stats[c.method+"(error)"]++
		} else if want.OK == nil {
			stats[c.method+"(None)"]++
		}
	}
	t.Logf("cases per method: %v", stats)
}

func normalizeAny(v any) any {
	b, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

func pcTrim(b []byte) string {
	if len(b) > 1500 {
		return string(b[:1500]) + "..."
	}
	return string(b)
}

func pcUnion(a, b map[string]string) map[string]bool {
	m := map[string]bool{}
	for k := range a {
		m[k] = true
	}
	for k := range b {
		m[k] = true
	}
	return m
}

func pcDiff(got, want string) string {
	gl, wl := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(gl) || i < len(wl); i++ {
		var g, w string
		if i < len(gl) {
			g = gl[i]
		}
		if i < len(wl) {
			w = wl[i]
		}
		if g != w {
			return fmt.Sprintf(" first difference at line %d\n Go:     %q\n Python: %q", i+1, g, w)
		}
	}
	return " (identical text)"
}
