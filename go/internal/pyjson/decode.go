package pyjson

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
)

// DecodeError is Python's json.JSONDecodeError: the message, and the position
// (in characters) in the document. Error() gives str(e).
type DecodeError struct {
	Msg string
	Doc []rune
	Pos int
}

func (e *DecodeError) Error() string {
	pos := e.Pos
	if pos > len(e.Doc) {
		pos = len(e.Doc)
	}
	lineno := 1
	last := -1
	for i := 0; i < pos; i++ {
		if e.Doc[i] == '\n' {
			lineno++
			last = i
		}
	}
	colno := pos - last
	return fmt.Sprintf("%s: line %d column %d (char %d)", e.Msg, lineno, colno, e.Pos)
}

// UnicodeDecodeError is Python's UnicodeDecodeError for the utf-8 codec.
type UnicodeDecodeError struct {
	Start, End int // End is exclusive
	Byte       byte
	Reason     string
}

func (e *UnicodeDecodeError) Error() string {
	if e.End-e.Start == 1 {
		return fmt.Sprintf("'utf-8' codec can't decode byte 0x%02x in position %d: %s", e.Byte, e.Start, e.Reason)
	}
	return fmt.Sprintf("'utf-8' codec can't decode bytes in position %d-%d: %s", e.Start, e.End-1, e.Reason)
}

// CheckUTF8 reports the first invalid sequence the way Python's utf-8 decoder
// does: the offending bytes are the longest valid prefix of a sequence. With
// surrogatePass, encoded surrogates (ED A0..BF xx) are accepted, as the
// "surrogatepass" error handler of json.loads(bytes) does.
func CheckUTF8(data []byte, surrogatePass bool) *UnicodeDecodeError {
	n := len(data)
	for i := 0; i < n; {
		b := data[i]
		if b < 0x80 {
			i++
			continue
		}
		var need int
		lo, hi := byte(0x80), byte(0xBF)
		switch {
		case b >= 0xC2 && b <= 0xDF:
			need = 1
		case b >= 0xE0 && b <= 0xEF:
			need = 2
			if b == 0xE0 {
				lo = 0xA0
			} else if b == 0xED {
				if surrogatePass && i+2 < n && data[i+1] >= 0xA0 && data[i+1] <= 0xBF && data[i+2] >= 0x80 && data[i+2] <= 0xBF {
					i += 3 // an encoded surrogate, accepted by "surrogatepass"
					continue
				}
				hi = 0x9F
			}
		case b >= 0xF0 && b <= 0xF4:
			need = 3
			if b == 0xF0 {
				lo = 0x90
			} else if b == 0xF4 {
				hi = 0x8F
			}
		default:
			return &UnicodeDecodeError{Start: i, End: i + 1, Byte: b, Reason: "invalid start byte"}
		}
		for j := 1; j <= need; j++ {
			if i+j >= n {
				return &UnicodeDecodeError{Start: i, End: n, Byte: b, Reason: "unexpected end of data"}
			}
			c := data[i+j]
			l, h := byte(0x80), byte(0xBF)
			if j == 1 {
				l, h = lo, hi
			}
			if c < l || c > h {
				return &UnicodeDecodeError{Start: i, End: i + j, Byte: b, Reason: "invalid continuation byte"}
			}
		}
		i += need + 1
	}
	return nil
}

// Load decodes a document like json.loads(bytes): a UTF-8 BOM is skipped (the
// utf-8-sig detection of json.detect_encoding). UTF-16/32 documents are not
// supported. Objects become *plist.Dict to keep key order, arrays become []any,
// integers become int64 or *big.Int, and other numbers become float64. NaN,
// Infinity and -Infinity are accepted, as in Python.
func Load(data []byte) (any, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")) // utf-8-sig
	if uerr := CheckUTF8(data, true); uerr != nil {
		return nil, uerr
	}
	return decodeText(decodeWTF8(data))
}

// LoadFile decodes a file the way json.load(open(path, "r")) does: UTF-8 with
// universal newlines (\r\n and \r become \n), and a leading BOM is an error.
func LoadFile(data []byte) (any, error) {
	if uerr := CheckUTF8(data, false); uerr != nil {
		return nil, uerr
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	doc := []rune(text)
	if len(doc) > 0 && doc[0] == '\ufeff' {
		return nil, &DecodeError{Msg: "Unexpected UTF-8 BOM (decode using utf-8-sig)", Doc: doc, Pos: 0}
	}
	return decodeText(doc)
}

// decodeWTF8 decodes UTF-8 that may contain encoded surrogates (already checked).
func decodeWTF8(data []byte) []rune {
	out := make([]rune, 0, len(data))
	for i := 0; i < len(data); {
		if data[i] == 0xED && i+2 < len(data) && data[i+1] >= 0xA0 {
			out = append(out, rune(0xD000)|rune(data[i+1]&0x3F)<<6|rune(data[i+2]&0x3F))
			i += 3
			continue
		}
		r, size := utf8.DecodeRune(data[i:])
		out = append(out, r)
		i += size
	}
	return out
}

// writeRune appends r, encoding a surrogate as three bytes (WTF-8), so a lone
// surrogate survives in a Go string like it does in a Python str.
func writeRune(b *strings.Builder, r rune) {
	if r >= 0xD800 && r <= 0xDFFF {
		b.WriteByte(0xED)
		b.WriteByte(0x80 | byte(r>>6)&0x3F)
		b.WriteByte(0x80 | byte(r)&0x3F)
		return
	}
	b.WriteRune(r)
}

type decoder struct {
	s []rune
}

// stopIteration is the StopIteration(idx) the scanner raises when no value
// starts at idx; the top level turns it into "Expecting value".
type stopIteration struct{ idx int }

func (stopIteration) Error() string { return "StopIteration" }

func decodeText(doc []rune) (any, error) {
	d := &decoder{s: doc}
	idx := d.skipSpace(0)
	v, end, err := d.scan(idx)
	if err != nil {
		var stop stopIteration
		if errors.As(err, &stop) {
			return nil, &DecodeError{Msg: "Expecting value", Doc: doc, Pos: stop.idx}
		}
		return nil, err
	}
	end = d.skipSpace(end)
	if end != len(doc) {
		return nil, &DecodeError{Msg: "Extra data", Doc: doc, Pos: end}
	}
	return v, nil
}

func (d *decoder) errAt(msg string, pos int) error {
	return &DecodeError{Msg: msg, Doc: d.s, Pos: pos}
}

func (d *decoder) skipSpace(idx int) int {
	for idx < len(d.s) {
		switch d.s[idx] {
		case ' ', '\t', '\n', '\r':
			idx++
		default:
			return idx
		}
	}
	return idx
}

func (d *decoder) hasPrefix(idx int, lit string) bool {
	r := []rune(lit)
	if idx+len(r) > len(d.s) {
		return false
	}
	for i, c := range r {
		if d.s[idx+i] != c {
			return false
		}
	}
	return true
}

func (d *decoder) scan(idx int) (any, int, error) {
	n := len(d.s)
	if idx >= n {
		return nil, 0, stopIteration{idx}
	}
	switch d.s[idx] {
	case '"':
		s, end, err := d.scanString(idx + 1)
		return s, end, err
	case '{':
		return d.object(idx + 1)
	case '[':
		return d.array(idx + 1)
	case 'n':
		if d.hasPrefix(idx, "null") {
			return nil, idx + 4, nil
		}
	case 't':
		if d.hasPrefix(idx, "true") {
			return true, idx + 4, nil
		}
	case 'f':
		if d.hasPrefix(idx, "false") {
			return false, idx + 5, nil
		}
	case 'N':
		if d.hasPrefix(idx, "NaN") {
			return math.NaN(), idx + 3, nil
		}
	case 'I':
		if d.hasPrefix(idx, "Infinity") {
			return math.Inf(1), idx + 8, nil
		}
	case '-':
		if d.hasPrefix(idx, "-Infinity") {
			return math.Inf(-1), idx + 9, nil
		}
	}
	return d.number(idx)
}

func (d *decoder) object(idx int) (any, int, error) {
	n := len(d.s)
	obj := plist.NewDict()
	idx = d.skipSpace(idx)
	if idx >= n || d.s[idx] != '}' {
		for {
			if idx >= n || d.s[idx] != '"' {
				return nil, 0, d.errAt("Expecting property name enclosed in double quotes", idx)
			}
			key, next, err := d.scanString(idx + 1)
			if err != nil {
				return nil, 0, err
			}
			idx = d.skipSpace(next)
			if idx >= n || d.s[idx] != ':' {
				return nil, 0, d.errAt("Expecting ':' delimiter", idx)
			}
			idx = d.skipSpace(idx + 1)
			val, next, err := d.scan(idx)
			if err != nil {
				return nil, 0, err
			}
			obj.Set(key, val)
			idx = d.skipSpace(next)
			if idx < n && d.s[idx] == '}' {
				break
			}
			if idx >= n || d.s[idx] != ',' {
				return nil, 0, d.errAt("Expecting ',' delimiter", idx)
			}
			comma := idx
			idx = d.skipSpace(idx + 1)
			if idx < n && d.s[idx] == '}' {
				return nil, 0, d.errAt("Illegal trailing comma before end of object", comma)
			}
		}
	}
	return obj, idx + 1, nil
}

func (d *decoder) array(idx int) (any, int, error) {
	n := len(d.s)
	items := []any{}
	idx = d.skipSpace(idx)
	if idx >= n || d.s[idx] != ']' {
		for {
			val, next, err := d.scan(idx)
			if err != nil {
				return nil, 0, err
			}
			items = append(items, val)
			idx = d.skipSpace(next)
			if idx < n && d.s[idx] == ']' {
				break
			}
			if idx >= n || d.s[idx] != ',' {
				return nil, 0, d.errAt("Expecting ',' delimiter", idx)
			}
			comma := idx
			idx = d.skipSpace(idx + 1)
			if idx < n && d.s[idx] == ']' {
				return nil, 0, d.errAt("Illegal trailing comma before end of array", comma)
			}
		}
	}
	return items, idx + 1, nil
}

func hexVal(c rune) (int, bool) {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0'), true
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10, true
	}
	return 0, false
}

// scanString decodes a string whose opening quote is just before end. A lone
// surrogate is stored as its three-byte WTF-8 form.
func (d *decoder) scanString(end int) (string, int, error) {
	n := len(d.s)
	begin := end - 1
	var b strings.Builder
	for {
		next := end
		var c rune
		for ; next < n; next++ {
			c = d.s[next]
			if c == '"' || c == '\\' {
				break
			}
			if c <= 0x1f {
				return "", 0, d.errAt("Invalid control character at", next)
			}
		}
		if next >= n {
			return "", 0, d.errAt("Unterminated string starting at", begin)
		}
		for _, r := range d.s[end:next] {
			writeRune(&b, r)
		}
		next++
		if c == '"' {
			return b.String(), next, nil
		}
		if next == n {
			return "", 0, d.errAt("Unterminated string starting at", begin)
		}
		c = d.s[next]
		if c != 'u' {
			end = next + 1
			switch c {
			case '"', '\\', '/':
			case 'b':
				c = '\b'
			case 'f':
				c = '\f'
			case 'n':
				c = '\n'
			case 'r':
				c = '\r'
			case 't':
				c = '\t'
			default:
				return "", 0, d.errAt("Invalid \\escape", end-2)
			}
			writeRune(&b, c)
			continue
		}
		next++
		end = next + 4
		if end > n {
			return "", 0, d.errAt("Invalid \\uXXXX escape", next-1)
		}
		var u rune
		for ; next < end; next++ {
			v, ok := hexVal(d.s[next])
			if !ok {
				return "", 0, d.errAt("Invalid \\uXXXX escape", end-5)
			}
			u = u<<4 | rune(v)
		}
		if u >= 0xD800 && u <= 0xDBFF && end+6 < n && d.s[next] == '\\' && d.s[next+1] == 'u' {
			next += 2
			end += 6
			var u2 rune
			for ; next < end; next++ {
				v, ok := hexVal(d.s[next])
				if !ok {
					return "", 0, d.errAt("Invalid \\uXXXX escape", end-5)
				}
				u2 = u2<<4 | rune(v)
			}
			if u2 >= 0xDC00 && u2 <= 0xDFFF {
				u = 0x10000 + (u-0xD800)<<10 + (u2 - 0xDC00)
			} else {
				end -= 6
			}
		}
		writeRune(&b, u)
	}
}

func isDigit(c rune) bool { return c >= '0' && c <= '9' }

// number follows CPython's C scanner (_match_number_unicode).
func (d *decoder) number(start int) (any, int, error) {
	n := len(d.s)
	idx := start
	if d.s[idx] == '-' {
		idx++
		if idx >= n {
			return nil, 0, stopIteration{start}
		}
	}
	switch {
	case d.s[idx] >= '1' && d.s[idx] <= '9':
		idx++
		for idx < n && isDigit(d.s[idx]) {
			idx++
		}
	case d.s[idx] == '0':
		idx++
	default:
		return nil, 0, stopIteration{start}
	}
	isFloat := false
	if idx+1 < n && d.s[idx] == '.' && isDigit(d.s[idx+1]) {
		isFloat = true
		idx += 2
		for idx < n && isDigit(d.s[idx]) {
			idx++
		}
	}
	if idx+1 < n && (d.s[idx] == 'e' || d.s[idx] == 'E') {
		eStart := idx
		idx++
		if idx+1 < n && (d.s[idx] == '-' || d.s[idx] == '+') {
			idx++
		}
		for idx < n && isDigit(d.s[idx]) {
			idx++
		}
		if isDigit(d.s[idx-1]) {
			isFloat = true
		} else {
			idx = eStart
		}
	}
	text := string(d.s[start:idx])
	if isFloat {
		f, err := strconv.ParseFloat(text, 64)
		if err != nil && !errors.Is(err, strconv.ErrRange) {
			return nil, 0, d.errAt("invalid number", start)
		}
		return f, idx, nil
	}
	v, ok := new(big.Int).SetString(text, 10)
	if !ok {
		return nil, 0, d.errAt("invalid number", start)
	}
	if v.IsInt64() {
		return v.Int64(), idx, nil
	}
	return v, idx, nil
}
