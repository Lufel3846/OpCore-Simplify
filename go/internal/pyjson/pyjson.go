// Package pyjson reproduces Python's json module as OpCore Simplify uses it:
// json.loads for downloaded metadata and history files, and json.dump with
// indent=4 for history.json and manifest.json.
//
// OpCore Simplify is distributed under the BSD 3-Clause License
// (Copyright (c) 2024, lzhoang2801). See go/LICENSE.
package pyjson

import (
	"errors"
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

// Load decodes a JSON document like json.loads. Objects become *plist.Dict to
// keep key order, arrays become []any, integers become int64 or *big.Int, and
// other numbers become float64. NaN, Infinity and -Infinity are accepted, as in
// Python.
func Load(data []byte) (any, error) {
	if !utf8.Valid(data) {
		return nil, errors.New("json: input is not valid UTF-8")
	}
	p := &decoder{s: string(data)}
	p.skipSpace()
	v, err := p.value()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.pos != len(p.s) {
		return nil, fmt.Errorf("json: extra data at position %d", p.pos)
	}
	return v, nil
}

type decoder struct {
	s   string
	pos int
}

func (d *decoder) skipSpace() {
	for d.pos < len(d.s) && strings.IndexByte(" \t\n\r", d.s[d.pos]) >= 0 {
		d.pos++
	}
}

func (d *decoder) fail(msg string) error {
	return fmt.Errorf("json: %s at position %d", msg, d.pos)
}

func (d *decoder) value() (any, error) {
	if d.pos >= len(d.s) {
		return nil, d.fail("expecting value")
	}
	switch c := d.s[d.pos]; {
	case c == '{':
		return d.object()
	case c == '[':
		return d.array()
	case c == '"':
		return d.str()
	case strings.HasPrefix(d.s[d.pos:], "null"):
		d.pos += 4
		return nil, nil
	case strings.HasPrefix(d.s[d.pos:], "true"):
		d.pos += 4
		return true, nil
	case strings.HasPrefix(d.s[d.pos:], "false"):
		d.pos += 5
		return false, nil
	case strings.HasPrefix(d.s[d.pos:], "NaN"):
		d.pos += 3
		return math.NaN(), nil
	case strings.HasPrefix(d.s[d.pos:], "Infinity"):
		d.pos += 8
		return math.Inf(1), nil
	case strings.HasPrefix(d.s[d.pos:], "-Infinity"):
		d.pos += 9
		return math.Inf(-1), nil
	case c == '-' || (c >= '0' && c <= '9'):
		return d.number()
	}
	return nil, d.fail("expecting value")
}

func (d *decoder) object() (any, error) {
	d.pos++ // '{'
	obj := plist.NewDict()
	d.skipSpace()
	if d.pos < len(d.s) && d.s[d.pos] == '}' {
		d.pos++
		return obj, nil
	}
	for {
		d.skipSpace()
		if d.pos >= len(d.s) || d.s[d.pos] != '"' {
			return nil, d.fail("expecting property name enclosed in double quotes")
		}
		key, err := d.str()
		if err != nil {
			return nil, err
		}
		d.skipSpace()
		if d.pos >= len(d.s) || d.s[d.pos] != ':' {
			return nil, d.fail("expecting ':' delimiter")
		}
		d.pos++
		d.skipSpace()
		val, err := d.value()
		if err != nil {
			return nil, err
		}
		obj.Set(key.(string), val)
		d.skipSpace()
		if d.pos < len(d.s) && d.s[d.pos] == ',' {
			d.pos++
			continue
		}
		if d.pos < len(d.s) && d.s[d.pos] == '}' {
			d.pos++
			return obj, nil
		}
		return nil, d.fail("expecting ',' delimiter")
	}
}

func (d *decoder) array() (any, error) {
	d.pos++ // '['
	items := []any{}
	d.skipSpace()
	if d.pos < len(d.s) && d.s[d.pos] == ']' {
		d.pos++
		return items, nil
	}
	for {
		d.skipSpace()
		v, err := d.value()
		if err != nil {
			return nil, err
		}
		items = append(items, v)
		d.skipSpace()
		if d.pos < len(d.s) && d.s[d.pos] == ',' {
			d.pos++
			continue
		}
		if d.pos < len(d.s) && d.s[d.pos] == ']' {
			d.pos++
			return items, nil
		}
		return nil, d.fail("expecting ',' delimiter")
	}
}

// str decodes a string literal. Control characters are rejected (strict mode).
func (d *decoder) str() (any, error) {
	d.pos++ // opening quote
	var b strings.Builder
	for {
		if d.pos >= len(d.s) {
			return nil, d.fail("unterminated string")
		}
		c := d.s[d.pos]
		switch {
		case c == '"':
			d.pos++
			return b.String(), nil
		case c < 0x20:
			return nil, d.fail("invalid control character in string")
		case c == '\\':
			d.pos++
			if d.pos >= len(d.s) {
				return nil, d.fail("unterminated string")
			}
			esc := d.s[d.pos]
			d.pos++
			switch esc {
			case '"', '\\', '/':
				b.WriteByte(esc)
			case 'b':
				b.WriteByte('\b')
			case 'f':
				b.WriteByte('\f')
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case 'u':
				r, err := d.hex4()
				if err != nil {
					return nil, err
				}
				if utf16.IsSurrogate(r) && d.pos+1 < len(d.s) && d.s[d.pos] == '\\' && d.s[d.pos+1] == 'u' {
					save := d.pos
					d.pos += 2
					r2, err := d.hex4()
					if err == nil && utf16.DecodeRune(r, r2) != utf8.RuneError {
						r = utf16.DecodeRune(r, r2)
					} else {
						d.pos = save
					}
				}
				b.WriteRune(r)
			default:
				return nil, d.fail("invalid \\escape")
			}
		default:
			_, size := utf8.DecodeRuneInString(d.s[d.pos:])
			b.WriteString(d.s[d.pos : d.pos+size])
			d.pos += size
		}
	}
}

func (d *decoder) hex4() (rune, error) {
	if d.pos+4 > len(d.s) {
		return 0, d.fail("invalid \\uXXXX escape")
	}
	n, err := strconv.ParseUint(d.s[d.pos:d.pos+4], 16, 32)
	if err != nil {
		return 0, d.fail("invalid \\uXXXX escape")
	}
	d.pos += 4
	return rune(n), nil
}

// number follows Python's NUMBER_RE: an integer, or a float when it has a
// fraction or exponent.
func (d *decoder) number() (any, error) {
	start := d.pos
	if d.s[d.pos] == '-' {
		d.pos++
	}
	if d.pos >= len(d.s) || !isDigit(d.s[d.pos]) {
		d.pos = start
		return nil, d.fail("expecting value")
	}
	if d.s[d.pos] == '0' {
		d.pos++
	} else {
		for d.pos < len(d.s) && isDigit(d.s[d.pos]) {
			d.pos++
		}
	}
	isFloat := false
	if d.pos+1 < len(d.s) && d.s[d.pos] == '.' && isDigit(d.s[d.pos+1]) {
		isFloat = true
		d.pos++
		for d.pos < len(d.s) && isDigit(d.s[d.pos]) {
			d.pos++
		}
	}
	if d.pos < len(d.s) && (d.s[d.pos] == 'e' || d.s[d.pos] == 'E') {
		p := d.pos + 1
		if p < len(d.s) && (d.s[p] == '+' || d.s[p] == '-') {
			p++
		}
		if p < len(d.s) && isDigit(d.s[p]) {
			isFloat = true
			for p < len(d.s) && isDigit(d.s[p]) {
				p++
			}
			d.pos = p
		}
	}
	text := d.s[start:d.pos]
	if isFloat {
		f, err := strconv.ParseFloat(text, 64)
		if err != nil && !errors.Is(err, strconv.ErrRange) {
			return nil, d.fail("invalid number")
		}
		return f, nil
	}
	n, ok := new(big.Int).SetString(text, 10)
	if !ok {
		return nil, d.fail("invalid number")
	}
	if n.IsInt64() {
		return n.Int64(), nil
	}
	return n, nil
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

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
	for _, r := range s {
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
