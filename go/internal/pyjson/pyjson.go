// Package pyjson reproduces Python's json module as OpCore Simplify uses it:
// json.loads for downloaded metadata and history files, and json.dump with
// indent=4 for history.json and manifest.json.
//
// OpCore Simplify is distributed under the BSD 3-Clause License
// (Copyright (c) 2024, lzhoang2801). See go/LICENSE.
package pyjson

import (
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
)

// Dump encodes v like json.dumps(v, indent=4) with ensure_ascii=True. Dict
// values keep their order; a map[string]any is written with sorted keys.
func Dump(v any) (string, error) {
	var b strings.Builder
	if err := encode(&b, v, 0); err != nil {
		return "", err
	}
	return b.String(), nil
}

func encode(b *strings.Builder, v any, level int) error {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if x {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case int:
		b.WriteString(strconv.Itoa(x))
	case int64:
		b.WriteString(strconv.FormatInt(x, 10))
	case *big.Int:
		b.WriteString(x.String())
	case float64:
		switch {
		case math.IsNaN(x):
			b.WriteString("NaN")
		case math.IsInf(x, 1):
			b.WriteString("Infinity")
		case math.IsInf(x, -1):
			b.WriteString("-Infinity")
		default:
			b.WriteString(plist.FormatFloat(x))
		}
	case string:
		writeString(b, x)
	case []any:
		if len(x) == 0 {
			b.WriteString("[]")
			return nil
		}
		b.WriteString("[")
		for i, item := range x {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString("\n" + strings.Repeat("    ", level+1))
			if err := encode(b, item, level+1); err != nil {
				return err
			}
		}
		b.WriteString("\n" + strings.Repeat("    ", level) + "]")
	case *plist.Dict:
		return encodeObject(b, x.Keys(), func(k string) any { v, _ := x.Get(k); return v }, level)
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return encodeObject(b, keys, func(k string) any { return x[k] }, level)
	default:
		return fmt.Errorf("json: object of type %T is not JSON serializable", v)
	}
	return nil
}

func encodeObject(b *strings.Builder, keys []string, get func(string) any, level int) error {
	if len(keys) == 0 {
		b.WriteString("{}")
		return nil
	}
	b.WriteString("{")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("\n" + strings.Repeat("    ", level+1))
		writeString(b, k)
		b.WriteString(": ")
		if err := encode(b, get(k), level+1); err != nil {
			return err
		}
	}
	b.WriteString("\n" + strings.Repeat("    ", level) + "}")
	return nil
}

// writeString mirrors json's ensure_ascii encoder: printable ASCII is kept,
// the escapes \" \\ \n \r \t \b \f are used, and everything else becomes
// \uXXXX (with surrogate pairs above the BMP), in lowercase hex.
func writeString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for i := 0; i < len(s); {
		var r rune
		if s[i] == 0xED && i+2 < len(s) && s[i+1] >= 0xA0 && s[i+1] <= 0xBF {
			// A lone surrogate stored as WTF-8.
			r = 0xD000 | rune(s[i+1]&0x3F)<<6 | rune(s[i+2]&0x3F)
			i += 3
		} else {
			var size int
			r, size = utf8.DecodeRuneInString(s[i:])
			i += size
		}
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r >= 0x20 && r <= 0x7e:
			b.WriteRune(r)
		case r < 0x10000:
			fmt.Fprintf(b, `\u%04x`, r)
		default:
			r1, r2 := utf16.EncodeRune(r)
			fmt.Fprintf(b, `\u%04x\u%04x`, r1, r2)
		}
	}
	b.WriteByte('"')
}
