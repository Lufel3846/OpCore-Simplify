package acpiguru

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/dsdt"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/dsdt/dsdttest"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
)

// paItem is one piece of a synthetic mixed listing: a source line or a run of
// AML bytes (printed as hex lines).
type paItem struct {
	text  string
	hex   []byte
	isHex bool
}

func paT(lvl int, s string) []paItem { return []paItem{{text: strings.Repeat("    ", lvl) + s}} }

func paH(b ...byte) []paItem { return []paItem{{hex: b, isHex: true}} }

func paHX(s string) []paItem {
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		panic(err)
	}
	return paH(b...)
}

func paCat(parts ...[]paItem) []paItem {
	var out []paItem
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func paBlock(lvl int, hdr string, body ...[]paItem) []paItem {
	return paCat(paT(lvl, hdr), paT(lvl, "{"), paCat(body...), paT(lvl, "}"))
}

type paTable struct {
	sig, oem, id string
	items        []paItem
}

// paBuild turns the items into raw table bytes and the mixed listing the fake
// iasl returns (the last hex line is omitted, as the real disassembler does).
func paBuild(t paTable) dsdttest.Table {
	items := append([]paItem{}, t.items...)
	var filler []byte
	for i := 0; i < 24; i++ {
		filler = append(filler, byte(0x31+i*7))
	}
	items = append(items, paItem{hex: filler, isHex: true})
	var body []byte
	type line struct {
		s     string
		isHex bool
	}
	var lines []line
	for _, it := range items {
		if !it.isHex {
			lines = append(lines, line{s: it.text})
			continue
		}
		for i := 0; i < len(it.hex); i += 16 {
			end := i + 16
			if end > len(it.hex) {
				end = len(it.hex)
			}
			var parts []string
			for _, b := range it.hex[i:end] {
				parts = append(parts, fmt.Sprintf("%02X", b))
			}
			lines = append(lines, line{s: fmt.Sprintf("    %04X: %s", 0x24+len(body)+i, strings.Join(parts, " ")), isHex: true})
		}
		body = append(body, it.hex...)
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i].isHex {
			lines = append(lines[:i], lines[i+1:]...)
			break
		}
	}
	hdr := make([]byte, 36)
	copy(hdr[0:4], t.sig)
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(36+len(body)))
	hdr[8] = 2
	copy(hdr[10:16], t.oem)
	copy(hdr[16:24], t.id)
	binary.LittleEndian.PutUint32(hdr[24:28], 0x01072009)
	copy(hdr[28:32], "INTL")
	binary.LittleEndian.PutUint32(hdr[32:36], 0x20230628)
	var sb strings.Builder
	sb.WriteString("/*\n * Intel ACPI Component Architecture\n * AML/ASL+ Disassembler version 20230628\n */\n")
	for _, l := range lines {
		sb.WriteString(l.s + "\n")
	}
	sb.WriteString("\nTable Header:\n    Signature        : \"" + t.sig + "\"\n")
	return dsdttest.Table{Raw: append(hdr, body...), DSL: sb.String()}
}

// ---- generators ----

type paGen struct {
	r   *rand.Rand
	cnt int
}

// uniq returns two bytes that make neighbouring patterns unique.
func (g *paGen) uniq() []byte {
	g.cnt++
	return []byte{byte(0x90 + g.cnt%0x50), byte(0x10 + (g.cnt*7)%0x70)}
}

func (g *paGen) pick(n int) int { return g.r.Intn(n) }

func (g *paGen) chance(p int) bool { return g.r.Intn(100) < p }

// sta builds an _STA (kind: none, method, name, stas, xsta, xstaname).
func (g *paGen) sta(lvl int, kind string) []paItem {
	u1, u2 := g.uniq(), g.uniq()
	switch kind {
	case "method", "stas", "xsta":
		name, tag := "_STA", []byte("_STA")
		if kind == "xsta" {
			name, tag = "XSTA", []byte("XSTA")
		}
		var ret []paItem
		if kind == "stas" {
			ret = paCat(
				paBlock(lvl+1, "If ((STAS == One))", paT(lvl+2, "Return (Zero)")),
				paT(lvl+1, "Return (0x0F)"))
		} else {
			ret = paT(lvl+1, "Return (0x0F)")
		}
		bytesOf := append(append(append([]byte{}, u1...), 0x14, 0x09), tag...)
		bytesOf = append(bytesOf, 0x00, 0xA4, 0x0A, 0x0F)
		bytesOf = append(bytesOf, u2...)
		return paCat(paT(lvl, "Method ("+name+", 0, NotSerialized)  // "+name+": Status"), paH(bytesOf...), paT(lvl, "{"), ret, paT(lvl, "}"))
	case "name", "xstaname":
		name, tag := "_STA", []byte("_STA")
		if kind == "xstaname" {
			name, tag = "XSTA", []byte("XSTA")
		}
		bytesOf := append(append([]byte{}, u1...), 0x08)
		bytesOf = append(bytesOf, tag...)
		bytesOf = append(bytesOf, 0x0A, 0x0F)
		bytesOf = append(bytesOf, u2...)
		return paCat(paT(lvl, "Name ("+name+", 0x0F)  // "+name+": Status"), paH(bytesOf...))
	}
	return nil
}

var paEndings = []string{
	"79 00",
	"86 09 00 01 00 00 D0 FE 00 04 00 00 79 00",
	"47 01 70 00 70 00 01 08 79 00",
	"AA BB 79 00",
	"AA BB CC DD 86 09 00 01",
	"AA BB CC DD EE 79 00", // pad too long for the 8 chars allowed
	"0A 0B 0C 0D 0E 0F 10 11 12 13",
	"47 01 80 00 80 00 01 01 79 00",
	"11 22 47 01 00 00 00 00 00 00",
	"86 09 00 00 00 00 00 00 00 00 00 00",
}

func paMask(list string) int {
	m := 0
	for _, p := range strings.Split(strings.ReplaceAll(list, " ", ""), ",") {
		if n, err := strconv.Atoi(p); err == nil && n >= 0 && n < 16 {
			m |= 1 << n
		}
	}
	return m
}

func paIRQBytes(list string) []byte {
	m := paMask(list)
	return []byte{0x22, byte(m & 0xFF), byte(m >> 8)}
}

// irqRes emits IRQNoFlags () for each list; the descriptors plus a tail are one
// hex run placed right after the first {..} line (or one per descriptor).
func (g *paGen) irqRes(lvl int, lists []string, combined bool, ending string) []paItem {
	var out []paItem
	var all []byte
	for _, l := range lists {
		all = append(all, paIRQBytes(l)...)
	}
	tail, _ := hex.DecodeString(strings.ReplaceAll(ending, " ", ""))
	for i, l := range lists {
		out = append(out, paT(lvl, "IRQNoFlags ()")...)
		out = append(out, paT(lvl+1, "{"+l+"}")...)
		if combined {
			if i == 0 {
				out = append(out, paH(append(append([]byte{}, all...), tail...)...)...)
			}
		} else {
			b := paIRQBytes(l)
			if i == len(lists)-1 {
				b = append(b, tail...)
			}
			out = append(out, paH(b...)...)
		}
	}
	return out
}

func (g *paGen) memFixed(lvl int, variant int) []paItem {
	switch variant {
	case 0:
		return paCat(paT(lvl, "Memory32Fixed (ReadWrite,"), paT(lvl+1, "0xFED00000,         // Address Base"), paT(lvl+1, "0x00000400,         // Address Length"), paT(lvl+1, ")"))
	case 1:
		return paCat(paT(lvl, "Memory32Fixed (ReadWrite,"), paT(lvl+1, "0xFED10000,         // Address Base"), paT(lvl+1, "0x00001000,         // Address Length"), paT(lvl+1, ")"))
	case 2:
		return paCat(paT(lvl, "Memory32Fixed (ReadWrite,"), paT(lvl+1, "Zero,         // Address Base"), paT(lvl+1, "One,         // Address Length"), paT(lvl+1, ")"))
	case 3:
		return paCat(paT(lvl, "Memory32Fixed (ReadWrite,"), paT(lvl+1, "0xFED00000,         // Address Base"), paT(lvl+1, ")"))
	case 4:
		return paCat(paT(lvl, "Memory32Fixed (ReadWrite,"), paT(lvl+1, "BASE,         // Address Base"), paT(lvl+1, "0x400,"), paT(lvl+1, ")"))
	case 5:
		return paCat(paT(lvl, "Memory32Fixed (ReadWrite,"), paT(lvl+1, "0x_FED0_0000 ,"), paT(lvl+1, "0X0400"), paT(lvl+1, ")"))
	default:
		return paT(lvl, "Memory32Fixed (ReadWrite, 0xFED00000, 0x400)")
	}
}

type paHPET struct {
	sta, crs   string // crs: none, name, method
	mem        int
	irqs       []string
	ending     string
	combined   bool
	hidForm    int
	dupBlock   bool
	crsBodyHex bool
}

func (g *paGen) hpet(lvl int, o paHPET) []paItem {
	hid := paCat(paT(lvl+1, `Name (_HID, EisaId ("PNP0103"))  // _HID: Hardware ID`))
	switch o.hidForm {
	case 1:
		hid = paT(lvl+1, `Name (_HID, "PNP0103")  // _HID: Hardware ID`)
	case 2:
		hid = paT(lvl+1, `Name (_HID, EisaId ("PNP0103") /* HPET System Timer */)`)
	}
	var crs []paItem
	if o.crs != "none" {
		u := g.uniq()
		u2 := g.uniq()
		head := append(append(append([]byte{}, u...), 0x08, 0x5F, 0x43, 0x52, 0x53, 0x11, 0x0D, 0x0A), u2...)
		body := paCat(g.irqRes(lvl+2, o.irqs, o.combined, o.ending), g.memFixed(lvl+2, o.mem))
		if o.dupBlock {
			body = paCat(body, paHX("22 01 09 79 00 11 22 22 01 09 79 00"))
		}
		if o.crs == "name" {
			crs = paCat(paT(lvl+1, "Name (_CRS, ResourceTemplate ()  // _CRS: Current Resource Settings"), paH(head...), paT(lvl+1, "{"), body, paT(lvl+1, "})"))
		} else {
			crs = paCat(paT(lvl+1, "Method (_CRS, 0, NotSerialized)  // _CRS: Current Resource Settings"), paH(head...), paT(lvl+1, "{"),
				paT(lvl+2, "Name (BUF0, ResourceTemplate ()"), paT(lvl+2, "{"), body, paT(lvl+2, "})"), paT(lvl+2, "Return (BUF0)"), paT(lvl+1, "}"))
		}
	}
	return paBlock(lvl, "Device (HPET)", hid, paT(lvl+1, `Name (_CID, EisaId ("PNP0C01"))  // _CID: Compatible ID`), g.sta(lvl+1, o.sta), crs)
}

type paRTC struct {
	name     string
	sta      string
	crs      string // none, buf, method
	ranges   [][2]int
	hidForm  int
	irqs     []string
	ending   string
	combined bool
	jumble   bool
}

func (g *paGen) rtc(lvl int, o paRTC) []paItem {
	hid := paT(lvl+1, `Name (_HID, EisaId ("PNP0B00"))  // _HID: Hardware ID`)
	switch o.hidForm {
	case 1:
		hid = paT(lvl+1, `Name (_HID, "pnp0b00")`)
	case 2:
		hid = paT(lvl+1, `Name (_HID, "PNP0B00")`)
	}
	var crs []paItem
	if o.crs != "none" {
		var io []paItem
		for _, rg := range o.ranges {
			io = paCat(io, paT(lvl+3, "IO (Decode16,"),
				paT(lvl+4, fmt.Sprintf("0x%04X,             // Range Minimum", rg[0])),
				paT(lvl+4, fmt.Sprintf("0x%04X,             // Range Maximum", rg[0])),
				paT(lvl+4, "0x01,               // Alignment"),
				paT(lvl+4, fmt.Sprintf("0x%02X,               // Length", rg[1])),
				paT(lvl+4, ")"))
		}
		irq := g.irqRes(lvl+3, o.irqs, o.combined, o.ending)
		head := append(append(g.uniq(), 0x08, 0x5F, 0x43, 0x52, 0x53, 0x11, 0x0D, 0x0A), g.uniq()...)
		if o.crs == "buf" {
			if o.jumble {
				// the buffer opens on the same line as the name, the way some tables look
				crs = paCat(paT(lvl+2, "Name (_CRS, ResourceTemplate ()  // _CRS: Current Resource Settings"), paH(head...), paT(lvl+2, "{"), io, irq, paT(lvl+2, "})"))
				crs = append(crs[:3:3], append(paT(0, "{ // joined"), crs[3:]...)...)
			} else {
				crs = paCat(paT(lvl+2, "Name (_CRS, ResourceTemplate ()  // _CRS: Current Resource Settings"), paH(head...), paT(lvl+2, "{"), io, irq, paT(lvl+2, "})"))
			}
		} else {
			crs = paCat(paT(lvl+2, "Method (_CRS, 0, NotSerialized)  // _CRS: Current Resource Settings"), paH(head...), paT(lvl+2, "{"),
				paT(lvl+3, "Name (BUF0, ResourceTemplate ()"), paT(lvl+3, "{"), io, irq, paT(lvl+3, "})"), paT(lvl+3, "Return (BUF0)"), paT(lvl+2, "}"))
		}
		// the name lines were built one level deeper than the other members
		for i := range crs {
			if !crs[i].isHex {
				crs[i].text = strings.TrimPrefix(crs[i].text, "    ")
			}
		}
	}
	return paBlock(lvl, "Device ("+o.name+")", hid, g.sta(lvl+1, o.sta), crs)
}

func (g *paGen) awac(lvl int, sta string, hidForm int) []paItem {
	hid := paT(lvl+1, `Name (_HID, "ACPI000E")  // _HID: Hardware ID`)
	if hidForm == 1 {
		hid = paT(lvl+1, `Name (_HID, "acpi000e")`)
	}
	return paBlock(lvl, "Device (AWAC)", hid, paT(lvl+1, "Name (_UID, Zero)"), g.sta(lvl+1, sta))
}

func (g *paGen) timer(lvl int, name, hid string, irq string, ending string) []paItem {
	var dup []paItem
	if g.chance(25) {
		// the same descriptor twice in one run of hex: too many matches
		b := append(paIRQBytes(irq), 0x79, 0x00, 0x11, 0x22)
		b = append(b, paIRQBytes(irq)...)
		b = append(b, []byte{0x47, 0x01, 0x70}...)
		if g.chance(50) {
			b = append(b, 0x79, 0x00)
		}
		dup = paH(b...)
	}
	return paBlock(lvl, "Device ("+name+")",
		paT(lvl+1, `Name (_HID, EisaId ("`+hid+`"))  // _HID: Hardware ID`),
		paT(lvl+1, "Name (_CRS, ResourceTemplate ()  // _CRS: Current Resource Settings"),
		paT(lvl+1, "{"), g.irqRes(lvl+2, []string{irq}, false, ending), dup, paT(lvl+1, "})"))
}

var paStaKinds = []string{"none", "method", "name", "stas", "xsta", "xstaname"}

func paDefBlock() []paItem {
	return paT(0, `DefinitionBlock ("", "DSDT", 2, "ALASKA", "A M I ", 0x01072009)`)
}

// paGenDSDT builds a randomized but realistic DSDT.
func paGenDSDT(g *paGen) []paItem {
	var lpcBody []paItem
	lpcName := []string{"LPCB", "LPC0", "LPC", "SBRG", "PX40", "XYZ_", "ISA"}[g.pick(7)]
	lpcAdr := []string{"0x001F0000", "0x00140003", "0x00140004", "Zero", "0x001F0000"}[g.pick(5)]
	lvl := 3
	if g.chance(80) {
		lpcBody = paCat(lpcBody, paT(lvl+1, "Name (_ADR, "+lpcAdr+")  // _ADR: Address"))
	}
	if g.chance(40) {
		lpcBody = paCat(lpcBody, paT(lvl+1, `Name (_HID, "LPC0001")`))
	}
	ec := g.pick(4) // 0 none, 1 in LPC, 2 under PCI0, 3 in LPC with string form
	if ec == 1 || ec == 3 {
		hid := `Name (_HID, EisaId ("PNP0C09"))  // _HID: Hardware ID`
		if ec == 3 {
			hid = `Name (_HID, "PNP0C09")`
		}
		lpcBody = paCat(lpcBody, paBlock(lvl+1, "Device (EC0)", paT(lvl+2, hid), g.sta(lvl+2, []string{"none", "method"}[g.pick(2)])))
	}
	hasHPET := g.chance(70)
	hpetInLPC := g.chance(60)
	var hpet []paItem
	if hasHPET {
		o := paHPET{sta: paStaKinds[g.pick(6)], crs: []string{"name", "name", "method", "none"}[g.pick(4)], mem: g.pick(8),
			combined: g.chance(30), hidForm: g.pick(3), dupBlock: g.chance(10)}
		o.irqs = [][]string{{"0,8,11"}, {"0,8,11"}, {"0,2,8"}, {"2"}, {"5"}, {"0", "8"}, {""}, {"0,8,11", "11"}, {"3,4", "0", "8,9"}}[g.pick(9)]
		o.ending = paEndings[g.pick(len(paEndings))]
		if hpetInLPC {
			hpet = g.hpet(lvl+1, o)
		} else {
			hpet = g.hpet(lvl-1, o)
		}
		if hpetInLPC {
			lpcBody = paCat(lpcBody, hpet)
		}
	}
	if g.chance(60) {
		o := paRTC{name: []string{"RTC", "RTC0", "RTC1", "RTC_", "CLK0"}[g.pick(5)], sta: paStaKinds[g.pick(6)], hidForm: g.pick(3),
			irqs: [][]string{{"8"}, {"8"}, {"0,8"}, {"3"}, {"8", "0"}}[g.pick(5)], ending: paEndings[g.pick(len(paEndings))], combined: g.chance(30)}
		o.crs = []string{"none", "buf", "buf", "method"}[g.pick(4)]
		o.jumble = false
		nr := 1 + g.pick(3)
		base := 0x70
		for i := 0; i < nr; i++ {
			ln := 2 + g.pick(7)
			o.ranges = append(o.ranges, [2]int{base, ln})
			base += ln + g.pick(3)*g.pick(4)
		}
		lpcBody = paCat(lpcBody, g.rtc(lvl+1, o))
	}
	if g.chance(55) {
		lpcBody = paCat(lpcBody, g.timer(lvl+1, []string{"TIMR", "TMR", "TMR_"}[g.pick(3)], []string{"PNP0100", "PNP0100", "PNP0B00"}[g.pick(3)],
			[]string{"0", "0,8", "2", "11"}[g.pick(4)], paEndings[g.pick(len(paEndings))]))
	}
	if g.chance(20) {
		// two devices with identical bytes: the patch needs a pad to be unique
		for _, nm := range []string{"TMR", "TIMR"} {
			lpcBody = paCat(lpcBody, paBlock(lvl+1, "Device ("+nm+")", paT(lvl+2, `Name (_HID, EisaId ("PNP0100"))`),
				paT(lvl+2, "Name (_CRS, ResourceTemplate ()"), paT(lvl+2, "{"), paT(lvl+3, "IRQNoFlags ()"), paT(lvl+4, "{0}"),
				paHX("22 01 00 79 00"), paT(lvl+2, "})")))
		}
	}
	if g.chance(30) {
		// several descriptors apart from each other, each with its own ending
		lpcBody = paCat(lpcBody, paBlock(lvl+1, "Device (RTC)", paT(lvl+2, `Name (_HID, "PNP0B00")`),
			paT(lvl+2, "Name (_CRS, ResourceTemplate ()"), paT(lvl+2, "{"),
			paT(lvl+3, "IRQNoFlags ()"), paT(lvl+4, "{8}"), paHX("22 00 01 79 00"),
			paT(lvl+3, "IO (Decode16, 0x70, 0x70, 1, 2, )"),
			paT(lvl+3, "IRQNoFlags ()"), paT(lvl+4, "{0,8}"), paHX("22 01 01 47 01"),
			paT(lvl+3, "IRQNoFlags ()"), paT(lvl+4, "{11}"), paHX("22 00 08 86 09"),
			paT(lvl+3, "IRQNoFlags ()"), paT(lvl+4, "{}"), paHX("22 00 00 79 00"),
			paT(lvl+2, "})")))
	}
	if g.chance(40) {
		lpcBody = paCat(lpcBody, g.timer(lvl+1, []string{"PIC", "IPIC", "PIC_"}[g.pick(3)], "PNP0000", []string{"2", "2,11", "0"}[g.pick(3)], paEndings[g.pick(len(paEndings))]))
	}
	if g.chance(30) {
		// something with IRQs we do not target, and an IRQ-less device
		lpcBody = paCat(lpcBody, g.timer(lvl+1, "KBD", "PNP0303", "1", "79 00"))
		lpcBody = paCat(lpcBody, paBlock(lvl+1, "Device (DMAC)", paT(lvl+2, `Name (_HID, EisaId ("PNP0200"))`)))
	}
	if g.chance(15) {
		// a stray line between two IRQ descriptors of one device
		lpcBody = paCat(lpcBody, paBlock(lvl+1, "Device (RTC1)",
			paT(lvl+2, `Name (_HID, "PNP0B00")`),
			paT(lvl+2, "Name (_CRS, ResourceTemplate ()"), paT(lvl+2, "{"),
			g.irqRes(lvl+3, []string{"8"}, false, "AA BB CC"),
			paT(lvl+3, "IO (Decode16, 0x70, 0x70, 1, 2, )"),
			g.irqRes(lvl+3, []string{"0", "11"}, false, "79 00"),
			paT(lvl+3, "Name (XYZ, Zero)"),
			g.irqRes(lvl+3, []string{"2"}, true, "79 00"),
			paT(lvl+2, "})")))
	}
	pci0 := paCat(paT(lvl, `Name (_HID, EisaId ("PNP0A08"))  // _HID: Hardware ID`))
	if g.chance(85) {
		pci0 = paCat(pci0, paBlock(lvl, "Device ("+lpcName+")", lpcBody))
	}
	if ec == 2 {
		pci0 = paCat(pci0, paBlock(lvl, "Device (EC0)", paT(lvl+1, `Name (_HID, EisaId ("PNP0C09"))`)))
	}
	sb := paBlock(1, "Scope (\\_SB)", paBlock(2, "Device (PCI0)", pci0))
	if hasHPET && !hpetInLPC {
		sb = paCat(sb, paBlock(1, "Scope (\\_SB.PCI0)", hpet))
	}
	if g.chance(50) {
		sb = paCat(sb, paBlock(1, "Scope (\\_SB)", g.awac(2, paStaKinds[g.pick(6)], g.pick(2))))
	}
	if g.chance(20) {
		sb = paCat(sb, paBlock(1, "Scope (\\_SB)", paT(2, "Name (STAS, Zero)")))
	}
	if g.chance(60) {
		var cpus []paItem
		n := 1 + g.pick(4)
		for i := 0; i < n; i++ {
			cpus = paCat(cpus, paBlock(2, fmt.Sprintf("Processor (CPU%d, 0x%02X, 0x00001810, 0x06)", i, i)))
		}
		sb = paCat(sb, paBlock(1, "Scope (\\_PR)", cpus))
	}
	return paCat(paDefBlock(), paT(0, "{"), sb, paT(0, "}"))
}

type paCase struct {
	name    string
	tables  map[string]paTable
	compile string
	target  []int
	calls   []map[string]any
}

type paOut struct {
	Results []struct {
		OK      any    `json:"ok"`
		Error   bool   `json:"error"`
		Message string `json:"message"`
	} `json:"results"`
	Files map[string]string `json:"files"`
}

// paPlugTables builds table sets for enable_cpu_power_management.
func paPlugTables(g *paGen) map[string]paTable {
	tables := map[string]paTable{}
	mk := func(sig string, items []paItem) paTable {
		return paTable{sig: sig, oem: "ALASKA", id: "A M I   ", items: items}
	}
	cpuScope := func(prefix string, n int) []paItem {
		var cpus []paItem
		for i := 0; i < n; i++ {
			cpus = paCat(cpus, paBlock(2, fmt.Sprintf("Processor (%s%d, 0x%02X, 0x00001810, 0x06)", prefix, i, i)))
		}
		return paBlock(1, []string{"Scope (\\_PR)", "Scope (\\_PR_)", "Scope (\\_SB)"}[g.pick(3)], cpus)
	}
	acpi7 := func(n int, scheme string, uidKind int, taken []string, noUID int) []paItem {
		var devs []paItem
		for i := 0; i < n; i++ {
			nm := fmt.Sprintf("%s%d", scheme, i)
			body := paT(3, `Name (_HID, "ACPI0007")  // _HID: Hardware ID`)
			if i != noUID {
				switch uidKind {
				case 0:
					body = paCat(body, paT(3, fmt.Sprintf("Name (_UID, 0x%02X)  // _UID: Unique ID", i)))
				case 1:
					body = paCat(body, paT(3, fmt.Sprintf(`Name (_UID, "CPU%d")`, i)))
				case 2:
					if i == 0 {
						body = paCat(body, paT(3, "Name (_UID, Zero)"))
					} else if i == 1 {
						body = paCat(body, paT(3, "Name (_UID, One)"))
					} else {
						body = paCat(body, paT(3, fmt.Sprintf("Name (_UID, 0x%X)", i)))
					}
				case 3:
					body = paCat(body, paT(3, "Name (_UID, "))
				}
			}
			devs = paCat(devs, paBlock(2, "Device ("+nm+")", body))
		}
		for _, tk := range taken {
			devs = paCat(devs, paBlock(2, "Device ("+tk+")", paT(3, `Name (_ADR, Zero)`)))
		}
		return paBlock(1, "Scope (\\_SB)", devs)
	}
	body := func() []paItem {
		switch g.pick(6) {
		case 0:
			return cpuScope("CPU", 1+g.pick(6))
		case 1:
			return acpi7(1+g.pick(5), []string{"C00", "P00", "CP0", "PR0", "XYZ"}[g.pick(5)], g.pick(4), nil, -1)
		case 2:
			return acpi7(2+g.pick(3), "C00", 0, []string{"C000"}, g.pick(3)-1)
		case 3:
			return acpi7(1, "ABC", 0, []string{"C000", "CP00", "P000", "PR00", "CX00", "PX00"}[:1+g.pick(6)], -1)
		case 4:
			return acpi7(12+g.pick(8), "XY", g.pick(3), nil, -1) // more than 16 → two-digit hex names
		}
		return paT(1, "Name (ZZZ, Zero)")
	}
	n := 1 + g.pick(4)
	names := []string{"DSDT.aml", "SSDT.aml", "SSDT-1.aml", "SSDT-10.aml", "SSDT-2.aml", "APIC.aml"}
	for i := 0; i < n; i++ {
		nm := names[g.pick(len(names))]
		if i == 0 && g.chance(70) {
			nm = "DSDT.aml"
		}
		sig := "SSDT"
		switch {
		case strings.HasPrefix(nm, "DSDT"):
			sig = "DSDT"
		case strings.HasPrefix(nm, "APIC"):
			sig = "APIC"
		}
		tables[nm] = mk(sig, paCat(paDefBlock(), paT(0, "{"), body(), paT(0, "}")))
	}
	return tables
}

func paRepo(t *testing.T) string {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func paRunOracle(t *testing.T, cases []map[string]any, iasl string) []paOut {
	t.Helper()
	in, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("python3", "-I", "testdata/pyref_patches_a.py", paRepo(t), iasl)
	cmd.Stdin = bytes.NewReader(in)
	cmd.Env = os.Environ()
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("oracle failed: %v\n%s", err, errb.String())
	}
	var res []paOut
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("oracle output: %v\n%.500s", err, out.String())
	}
	return res
}

func paPairs(keys []string, get func(string) any) map[string]any {
	pairs := []any{}
	for _, k := range keys {
		pairs = append(pairs, []any{k, get(k)})
	}
	return map[string]any{"__dict__": pairs}
}

func paIntsAny(v []int) []any {
	out := []any{}
	for _, x := range v {
		out = append(out, x)
	}
	return out
}

func paGoCall(g *Guru, c map[string]any) (any, bool) {
	var d *plist.Dict
	var err error
	switch c["fn"] {
	case "plug":
		d, err = g.EnableCPUPowerManagement()
	case "irq":
		d, err = g.FixIRQConflicts()
	case "awac":
		d, err = g.FixSystemClockAWAC()
	case "list_irqs", "irq_choice":
		devs, err := g.ListIRQs()
		if err != nil {
			return nil, true
		}
		if c["fn"] == "irq_choice" {
			ch := g.GetIRQChoice(devs)
			var keys []string
			for _, k := range devs.Order {
				if _, ok := ch[k]; ok {
					keys = append(keys, k)
				}
			}
			return paPairs(keys, func(k string) any { return paIntsAny(ch[k]) }), false
		}
		return paPairs(devs.Order, func(k string) any {
			info := devs.M[k]
			pairs := []any{[]any{"irq", info.IRQ}}
			if info.HasHID {
				pairs = append(pairs, []any{"hid", info.HID})
			}
			return map[string]any{"__dict__": pairs}
		}), false
	case "hex_irqs":
		var rem []int
		if r, ok := c["rem"].([]int); ok {
			rem = r
		}
		lines, err := g.GetHexFromIRQs(c["irq"].(string), rem)
		if err != nil {
			return nil, true
		}
		out := []any{}
		for _, l := range lines {
			out = append(out, map[string]any{"__dict__": []any{
				[]any{"irq", l.IRQ}, []any{"find", l.Find}, []any{"repl", l.Repl},
				[]any{"remd", paIntsAny(l.Remd)}, []any{"index", l.Index}, []any{"changed", l.Changed}}})
		}
		return out, false
	case "int_line":
		out := []any{}
		for _, v := range g.GetIntForLine(c["irq"].(string)) {
			out = append(out, v)
		}
		return out, false
	case "conv_irq":
		return g.ConvertIRQToInt(c["irq"].(int)), false
	case "same_line":
		return g.SameLineIRQ(c["irq"].(string)), false
	default:
		panic("unknown fn")
	}
	if err != nil {
		return nil, true
	}
	if d == nil {
		return nil, false
	}
	return d, false
}

// paRun runs the cases through the oracle and through Go and compares.
func paRun(t *testing.T, cases []paCase) (nonNil map[string]int) {
	t.Helper()
	nonNil = map[string]int{}
	base := t.TempDir()
	iasl := filepath.Join(base, "iasl")
	if err := os.WriteFile(iasl, []byte(dsdttest.FakeIasl), 0o755); err != nil {
		t.Fatal(err)
	}
	var pyCases []map[string]any
	type dirs struct{ tables, dsl, outPy, outGo string }
	var dd []dirs
	for i, c := range cases {
		root := filepath.Join(base, fmt.Sprintf("c%d", i))
		d := dirs{filepath.Join(root, "tables"), filepath.Join(root, "dsl"), filepath.Join(root, "out-py"), filepath.Join(root, "out-go")}
		for _, p := range []string{d.tables, d.dsl} {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		for name, tb := range c.tables {
			b := paBuild(tb)
			if err := os.WriteFile(filepath.Join(d.tables, name), b.Raw, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(d.dsl, name+".dsl"), []byte(b.DSL), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		dd = append(dd, d)
		pc := map[string]any{"dsl_dir": d.dsl, "out": d.outPy, "compile": c.compile, "calls": c.calls}
		if len(c.tables) > 0 {
			pc["dir"] = d.tables
		}
		if c.target != nil {
			pc["target_irqs"] = c.target
		}
		pyCases = append(pyCases, pc)
	}
	py := paRunOracle(t, pyCases, iasl)
	if len(py) != len(cases) {
		t.Fatalf("oracle returned %d cases for %d", len(py), len(cases))
	}
	for i, c := range cases {
		d := dd[i]
		t.Setenv("FAKE_DSL_DIR", d.dsl)
		t.Setenv("FAKE_COMPILE", "")
		g := New(dsdt.New(iasl), func(string) (string, error) { return "", io.EOF })
		if c.target != nil {
			g.TargetIRQs = c.target
		}
		if len(c.tables) > 0 {
			if _, _, err := g.ReadACPITables(d.tables); err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			g.Dsdt = g.Acpi.GetDSDTOrOnly()
			if n, ok := g.GetLPCName(false, false, false); ok {
				g.LPCBusDevice = n
			}
		}
		g.ACPIDirectory = d.outGo
		t.Setenv("FAKE_COMPILE", c.compile)
		want := py[i]
		for k, call := range c.calls {
			got, gerr := paGoCall(g, call)
			w := want.Results[k]
			if gerr != w.Error {
				t.Errorf("%s call %d %v: Go error=%v, Python error=%v (%s)", c.name, k, call["fn"], gerr, w.Error, w.Message)
				continue
			}
			if gerr {
				continue
			}
			if gn := normalize(got); !reflect.DeepEqual(gn, w.OK) {
				gb, _ := json.Marshal(gn)
				wb, _ := json.Marshal(w.OK)
				t.Errorf("%s call %d %v:\n go: %.1500s\n py: %.1500s", c.name, k, call["fn"], gb, wb)
			} else if w.OK != nil {
				nonNil[fmt.Sprint(call["fn"])]++
				wb, _ := json.Marshal(w.OK)
				for _, f := range []string{"Generic IRQ Patch", " IRQ ", " of ", "_STA to XSTA", "_CRS to XCRS", "\"Enabled\",false", "\"Enabled\",true"} {
					if bytes.Contains(wb, []byte(f)) {
						nonNil["feature:"+f]++
					}
				}
			}
		}
		files := map[string]string{}
		if ents, err := os.ReadDir(d.outGo); err == nil {
			for _, e := range ents {
				b, _ := os.ReadFile(filepath.Join(d.outGo, e.Name()))
				files[e.Name()] = hex.EncodeToString(b)
			}
		}
		if len(files) != len(want.Files) {
			t.Errorf("%s: written files go=%v py=%v", c.name, keysOf(files), keysOf(want.Files))
		}
		for name, h := range want.Files {
			wb, _ := hex.DecodeString(h)
			for _, f := range []string{"Fake HPET", "Supplementary HPET", "RTC0", "ZSTA", "XCRS", "STAS", "BUFX", "Method (_CRS", "CpuPlugA", "CpuPlug\"", "Scope (\\_SB.PCI0.LPCB)", "Return (Zero)"} {
				if bytes.Contains(wb, []byte(f)) {
					nonNil["file:"+f]++
				}
			}
			if files[name] != h {
				gb, _ := hex.DecodeString(files[name])
				wb, _ := hex.DecodeString(h)
				t.Errorf("%s: file %s differs:\n--- go\n%s\n--- py\n%s", c.name, name, gb, wb)
			}
		}
	}
	return nonNil
}

func keysOf[V any](m map[string]V) []string {
	var k []string
	for x := range m {
		k = append(k, x)
	}
	sort.Strings(k)
	return k
}

func paCalls(fns ...string) []map[string]any {
	var out []map[string]any
	for _, f := range fns {
		out = append(out, map[string]any{"fn": f})
	}
	return out
}

func TestPatchesAParityDSDT(t *testing.T) {
	var cases []paCase
	for i := 0; i < 260; i++ {
		g := &paGen{r: rand.New(rand.NewSource(int64(1000 + i)))}
		items := paGenDSDT(g)
		compile := []string{"ok", "ok", "ok", "fail", ""}[g.pick(5)]
		var extra map[string]paTable
		if g.chance(15) { // a second table shifts the table order/LPC lookups
			extra = map[string]paTable{"SSDT.aml": {sig: "SSDT", oem: "PmRef ", id: "CpuPm   ",
				items: paCat(paDefBlock(), paT(0, "{"), paBlock(1, "Scope (\\_SB)", paBlock(2, "Device (EC1)", paT(3, `Name (_HID, EisaId ("PNP0C09"))`))), paT(0, "}"))}}
		}
		tables := map[string]paTable{"DSDT.aml": {sig: "DSDT", oem: "ALASKA", id: "A M I   ", items: items}}
		for k, v := range extra {
			tables[k] = v
		}
		c := paCase{name: fmt.Sprintf("dsdt%d", i), tables: tables, compile: compile,
			calls: paCalls("list_irqs", "irq_choice", "irq", "awac", "plug")}
		switch i % 13 {
		case 6:
			c.target = []int{-1, 0, 8}
		case 7:
			c.target = []int{-2, -1, 8, 11, 0}
		case 8:
			c.target = []int{20, 0, 8}
		case 9:
			c.target = []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
		case 10:
			c.target = []int{70, 8, 0}
		case 11:
			c.target = []int{15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1, 0, 0, 8, 8}
		case 3:
			c.target = []int{0, 8}
		case 4:
			c.target = []int{11, 0, 8, 2, 8, 0}
		case 5:
			c.target = []int{}
		}
		cases = append(cases, c)
	}
	nn := paRun(t, cases)
	t.Logf("non-nil results: %v", nn)
	for _, fn := range []string{"irq", "awac", "plug", "list_irqs"} {
		if nn[fn] < 15 {
			t.Errorf("only %d non-nil %s results: generator does not exercise it", nn[fn], fn)
		}
	}
}

func TestPatchesAParityPlug(t *testing.T) {
	var cases []paCase
	for i := 0; i < 150; i++ {
		g := &paGen{r: rand.New(rand.NewSource(int64(5000 + i)))}
		cases = append(cases, paCase{name: fmt.Sprintf("plug%d", i), tables: paPlugTables(g),
			compile: []string{"ok", "ok", "fail", ""}[i%4], calls: paCalls("plug")})
	}
	nn := paRun(t, cases)
	t.Logf("non-nil results: %v", nn)
	if nn["plug"] < 40 {
		t.Errorf("only %d non-nil plug results", nn["plug"])
	}
}

func TestPatchesAParityPure(t *testing.T) {
	var calls []map[string]any
	irqs := []string{"", "#", "0", "8", "0,8,11", "0, 8", "15", "16", "-1", "abc", "1,#,3", "0:8", "0,8:11", "2:#:5,6", "::", " 5 ", "+3", "1_0", "0x5", "3.0", "0,0,0", "15,15", "99999999999999999999,2", "7,x,9"}
	for _, s := range irqs {
		calls = append(calls, map[string]any{"fn": "same_line", "irq": s}, map[string]any{"fn": "int_line", "irq": s})
	}
	for n := -3; n < 63; n++ {
		calls = append(calls, map[string]any{"fn": "conv_irq", "irq": n})
	}
	rems := [][]int{nil, {}, {0}, {0, 2, 8, 11}, {11, 8, 2, 0}, {8, 8}, {15}, {16}, {-1}, {1, 3, 5, 7, 9}, {5, 5, 5, 3, 3, 12}, {0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}}
	hexIn := []string{"12|0,8,11", "5|8", "5|0:8", "3|0,8,11-9|2", "3|0,8,11-9|2:#-11|#", "0|", "x|1", "5", "5|1|2", "1|3-2|4:5,6-3|15", "07|8", " 4|8", "4 |8", "-5|8", "1|16", "1|0,2,8,11:0,8:2"}
	for _, h := range hexIn {
		for _, r := range rems {
			c := map[string]any{"fn": "hex_irqs", "irq": h}
			if r != nil {
				c["rem"] = r
			}
			calls = append(calls, c)
		}
	}
	// the harness passes rem as []int to Go and JSON to Python
	cases := []paCase{{name: "pure", calls: calls}}
	runPure(t, cases)
}

// runPure is paRun for calls without tables (the call list is shared).
func runPure(t *testing.T, cases []paCase) {
	paRun(t, cases)
}

func TestPatchesAParityListIRQsNoDSDT(t *testing.T) {
	cases := []paCase{{name: "nodsdt", calls: paCalls("list_irqs", "irq_choice", "irq", "awac", "plug")}}
	paRun(t, cases)
}
