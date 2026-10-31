// Package dsdttest builds synthetic ACPI tables and a fake iasl for tests.
package dsdttest

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"strings"
)

// fakeIasl copies FAKE_DSL_DIR/<table>.dsl to <base>.dsl for each table argument.
// FAKE_FAIL_DA=1 makes the call that carries -da fail; FAKE_FAIL_MULTI=1 makes
// calls with more than one table fail. Both sides run this same script.
const FakeIasl = `#!/bin/sh
if [ "$FAKE_FAIL_DA" = "1" ]; then
  for a in "$@"; do [ "$a" = "-da" ] && exit 1; done
fi
if [ -n "$FAKE_COMPILE" ]; then
  for a in "$@"; do
    case "$a" in
      *.dsl)
        [ "$FAKE_COMPILE" = "fail" ] && exit 1
        [ "$FAKE_COMPILE" = "ok" ] && cp "$a" "${a%.dsl}.aml"
        exit 0 ;;
    esac
  done
fi
n=0
for a in "$@"; do case "$a" in -*) ;; *) n=$((n+1));; esac; done
if [ "$FAKE_FAIL_MULTI" = "1" ] && [ "$n" -gt 1 ]; then exit 1; fi
for a in "$@"; do
  case "$a" in
    -*) ;;
    *) b="${a%.*}"
       if [ -f "$FAKE_DSL_DIR/$a.dsl" ]; then cp "$FAKE_DSL_DIR/$a.dsl" "$b.dsl"; fi ;;
  esac
done
exit 0
`

// textLines is the disassembly body; hex lines are interleaved by buildTable.
var TextLines = []string{
	`DefinitionBlock ("", "DSDT", 2, "ALASKA", "A M I ", 0x01072009)`,
	`{`,
	`    External (_SB_.PCI0.XHC_, DeviceObj)    // (from opcode)`,
	`    Scope (\_SB)`,
	`    {`,
	`        Device (PCI0)`,
	`        {`,
	`            Name (_HID, EisaId ("PNP0A08"))  // _HID: Hardware ID`,
	`            Method (_STA, 0, NotSerialized)  // _STA: Status`,
	`            {`,
	`                Return (0x0F)`,
	`            }`,
	`            Device (LPCB)`,
	`            {`,
	`                Name (_ADR, 0x001F0000)  // _ADR: Address`,
	`                Device (EC0_)`,
	`                {`,
	`                    Name (_HID, "ACPI0001")  // _HID: Hardware ID`,
	`                }`,
	`            }`,
	`            Device (^SAT0)`,
	`            {`,
	`                Name (_ADR, 0x00170000)  // _ADR: Address`,
	`            }`,
	`        }`,
	`        Scope (PCI0.LPCB)`,
	`        {`,
	`            Device (HPET)`,
	`            {`,
	`                Name (_HID, EisaId ("PNP0103"))  // _HID: Hardware ID`,
	`            }`,
	`        }`,
	`        Device (\_SB.USB0)`,
	`        {`,
	`            Name (_HID, "ACPI000E")`,
	`        }`,
	`    }`,
	`    Scope (\_PR)`,
	`    {`,
	`        Processor (CPU0, 0x01, 0x00001810, 0x06)`,
	`        {`,
	`        }`,
	`    }`,
	`    Method (_WAK, 1, Serialized)  // _WAK: Wake`,
	`    {`,
	`        Return (Package (0x02) { Zero, Zero })`,
	`    }`,
	`}`,
}

// Table is a generated table: the raw bytes and the matching mixed listing.
type Table struct {
	Raw []byte
	DSL string
}

// TableOpts describes a generated table.
type TableOpts struct {
	Sig      string
	OEM      string
	ID       string
	CRLF     bool
	BodySize int
	Seed     int64
	// Lines is the disassembly text; nil uses TextLines.
	Lines []string
}

// BuildTable generates raw AML bytes and the matching mixed listing.
func BuildTable(o TableOpts) Table {
	rng := rand.New(rand.NewSource(o.Seed))
	body := make([]byte, o.BodySize)
	for i := range body {
		body[i] = byte(rng.Intn(256))
	}
	// Repeated sequences so that a pad is needed to make a pattern unique.
	for _, at := range []int{20, 100, 180, 260} {
		if at+8 <= len(body) {
			copy(body[at:], []byte{0x5C, 0x5F, 0x53, 0x42, 0x5F, 0x50, 0x43, 0x49})
		}
	}
	hdr := make([]byte, 36)
	copy(hdr[0:4], o.Sig)
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(36+len(body)))
	hdr[8] = 2
	copy(hdr[10:16], o.OEM)
	copy(hdr[16:24], o.ID)
	binary.LittleEndian.PutUint32(hdr[24:28], 0x01072009)
	copy(hdr[28:32], "INTL")
	binary.LittleEndian.PutUint32(hdr[32:36], 0x20230628)
	raw := append(hdr, body...)

	// Hex chunks: every 16-byte line of the body except the last one, which the
	// disassembler omits.
	var chunks []string
	n := (len(body) + 15) / 16
	for i := 0; i < n-1; i++ {
		c := body[i*16 : i*16+16]
		var parts []string
		for _, b := range c {
			parts = append(parts, fmt.Sprintf("%02X", b))
		}
		chunks = append(chunks, fmt.Sprintf("    %04X: %s", 0x24+i*16, strings.Join(parts, " ")))
	}
	var sb strings.Builder
	sb.WriteString("/*\n * Intel ACPI Component Architecture\n * AML/ASL+ Disassembler version 20230628\n */\n")
	ci := 0
	text := o.Lines
	if text == nil {
		text = TextLines
	}
	for i, l := range text {
		sb.WriteString(l + "\n")
		for k := 0; k < i%3 && ci < len(chunks); k++ {
			c := chunks[ci]
			if k == 0 && i%5 == 0 {
				c += "  // commented tail"
			}
			sb.WriteString(c + "\n")
			ci++
		}
	}
	for ; ci < len(chunks); ci++ {
		sb.WriteString(chunks[ci] + "\n")
	}
	sb.WriteString("\nTable Header:\n    Signature        : \"" + o.Sig + "\"\n")
	dsl := sb.String()
	if o.CRLF {
		dsl = strings.ReplaceAll(dsl, "\n", "\r\n")
	}
	return Table{Raw: raw, DSL: dsl}
}
