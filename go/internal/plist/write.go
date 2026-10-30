package plist

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"
)

// plistHeader is plistlib.PLISTHEADER.
const plistHeader = "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n" +
	"<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n"

// Marshal encodes v as an XML property list, byte-for-byte like
// plistlib.dumps(v, fmt=FMT_XML) with sort_keys=True.
//
// Supported values: *Dict, map[string]any (keys sorted), []any, string, bool,
// int, int64, uint64, *big.Int (range -2^63 .. 2^64-1), float64, []byte,
// time.Time (naive, written in UTC wall-clock fields). Other types are an error,
// like Python's TypeError.
func Marshal(v any) ([]byte, error) {
	w := &xmlWriter{}
	w.buf.WriteString(plistHeader)
	w.writeln(`<plist version="1.0">`)
	if err := w.writeValue(v); err != nil {
		return nil, err
	}
	w.writeln("</plist>")
	return w.buf.Bytes(), nil
}

type xmlWriter struct {
	buf   bytes.Buffer
	level int
}

const indent = "\t"

func (w *xmlWriter) writeln(line string) {
	if line != "" {
		w.buf.WriteString(strings.Repeat(indent, w.level))
		w.buf.WriteString(line)
	}
	w.buf.WriteByte('\n')
}

func (w *xmlWriter) begin(element string) {
	w.writeln("<" + element + ">")
	w.level++
}

func (w *xmlWriter) end(element string) {
	w.level--
	w.writeln("</" + element + ">")
}

// simple mirrors _DumbXMLWriter.simple_element. A nil value writes <el/>.
func (w *xmlWriter) simple(element string, value *string) error {
	if value == nil {
		w.writeln("<" + element + "/>")
		return nil
	}
	escaped, err := escape(*value)
	if err != nil {
		return err
	}
	w.writeln("<" + element + ">" + escaped + "</" + element + ">")
	return nil
}

func text(s string) *string { return &s }

// escape mirrors plistlib._escape: control characters are rejected, line
// endings are normalized to \n, and &, < and > are escaped.
func escape(s string) (string, error) {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c <= 0x08) || c == 0x0b || c == 0x0c || (c >= 0x0e && c <= 0x1f) {
			return "", errors.New("strings can't contain control characters; use bytes instead")
		}
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s, nil
}

func (w *xmlWriter) writeValue(v any) error {
	switch x := v.(type) {
	case string:
		return w.simple("string", text(x))
	case bool:
		if x {
			return w.simple("true", nil)
		}
		return w.simple("false", nil)
	case int:
		return w.simple("integer", text(fmt.Sprintf("%d", x)))
	case int64:
		return w.simple("integer", text(fmt.Sprintf("%d", x)))
	case uint64:
		return w.simple("integer", text(fmt.Sprintf("%d", x)))
	case *big.Int:
		if x.Cmp(minInt64) < 0 || x.Cmp(maxUint64) > 0 {
			return fmt.Errorf("integer out of range: %s", x)
		}
		return w.simple("integer", text(x.String()))
	case float64:
		return w.simple("real", text(pyFloatRepr(x)))
	case *Dict:
		return w.writeDict(x)
	case map[string]any:
		d := NewDict()
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			d.Set(k, x[k])
		}
		return w.writeDict(d)
	case []byte:
		w.writeBytes(x)
		return nil
	case time.Time:
		return w.simple("date", text(fmt.Sprintf("%04d-%02d-%02dT%02d:%02d:%02dZ",
			x.Year(), int(x.Month()), x.Day(), x.Hour(), x.Minute(), x.Second())))
	case []any:
		return w.writeArray(x)
	}
	return fmt.Errorf("unsupported type: %T", v)
}

func (w *xmlWriter) writeDict(d *Dict) error {
	if d.Len() == 0 {
		return w.simple("dict", nil)
	}
	w.begin("dict")
	keys := d.Keys()
	sort.Strings(keys)
	for _, k := range keys {
		value, _ := d.Get(k)
		if err := w.simple("key", text(k)); err != nil {
			return err
		}
		if err := w.writeValue(value); err != nil {
			return err
		}
	}
	w.end("dict")
	return nil
}

func (w *xmlWriter) writeArray(items []any) error {
	if len(items) == 0 {
		return w.simple("array", nil)
	}
	w.begin("array")
	for _, item := range items {
		if err := w.writeValue(item); err != nil {
			return err
		}
	}
	w.end("array")
	return nil
}

// writeBytes mirrors _PlistWriter.write_bytes: the data lines are written at the
// indentation of the <data> tag, and their width is limited to 76 columns
// (with a minimum of 16) counting the indentation.
func (w *xmlWriter) writeBytes(data []byte) {
	w.begin("data")
	w.level--
	maxLine := max(16, 76-8*w.level)
	for _, line := range base64Encode(data, maxLine) {
		w.writeln(line)
	}
	w.level++
	w.end("data")
}

var (
	minInt64  = big.NewInt(-1 << 63)
	maxUint64 = new(big.Int).SetUint64(^uint64(0))
)
