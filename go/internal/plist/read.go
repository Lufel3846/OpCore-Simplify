package plist

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Unmarshal decodes an XML property list, mirroring plistlib.loads with the
// XML parser of plistlib (_PlistParser). Decoded values are:
// *Dict, []any, string, bool, int64 or *big.Int, float64, []byte, time.Time.
// A document without a root value decodes to nil, as in Python.
//
// Binary property lists (the "bplist00" format) are not supported yet and are
// reported as an error; see go/README.md.
func Unmarshal(data []byte) (any, error) {
	if bytes.HasPrefix(data, []byte("bplist00")) {
		return nil, errors.New("binary property lists are not supported yet")
	}
	p := &parser{}
	return p.parse(data)
}

// parser is a direct port of plistlib._PlistParser. Arrays are collected as
// *array during parsing and converted to []any when the document is complete,
// because a Go slice stored in a parent cannot grow after insertion.
type parser struct {
	stack      []any // *Dict or *array
	currentKey string
	hasKey     bool
	data       []string
	root       any
}

type array struct {
	items []any
}

func (p *parser) parse(data []byte) (any, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			p.data = nil
			if err := p.begin(t.Name.Local); err != nil {
				return nil, err
			}
		case xml.EndElement:
			if err := p.end(t.Name.Local); err != nil {
				return nil, err
			}
		case xml.CharData:
			p.data = append(p.data, string(t))
		case xml.Directive:
			if strings.Contains(strings.ToUpper(string(t)), "ENTITY") {
				return nil, errors.New("XML entity declarations are not supported in plist files")
			}
		}
	}
	return freeze(p.root), nil
}

// text returns the character data collected since the element started, and
// clears it, like _PlistParser.get_data.
func (p *parser) text() string {
	s := strings.Join(p.data, "")
	p.data = nil
	return s
}

func (p *parser) begin(element string) error {
	switch element {
	case "dict":
		d := NewDict()
		if err := p.addObject(d); err != nil {
			return err
		}
		p.stack = append(p.stack, d)
	case "array":
		a := &array{}
		if err := p.addObject(a); err != nil {
			return err
		}
		p.stack = append(p.stack, a)
	}
	return nil
}

func (p *parser) end(element string) error {
	switch element {
	case "dict":
		// Python tests the truthiness of current_key, so an empty key is ignored here.
		if p.hasKey && p.currentKey != "" {
			return fmt.Errorf("missing value for key '%s'", p.currentKey)
		}
		p.pop()
	case "key":
		if _, isDict := p.top().(*Dict); (p.hasKey && p.currentKey != "") || !isDict {
			return errors.New("unexpected key")
		}
		p.currentKey = p.text()
		p.hasKey = true
	case "array":
		p.pop()
	case "true":
		return p.addObject(true)
	case "false":
		return p.addObject(false)
	case "integer":
		raw := p.text()
		base := 10
		if strings.HasPrefix(raw, "0x") || strings.HasPrefix(raw, "0X") {
			base = 16
		}
		n, err := pyInt(raw, base)
		if err != nil {
			return err
		}
		return p.addObject(n)
	case "real":
		f, err := pyFloat(p.text())
		if err != nil {
			return err
		}
		return p.addObject(f)
	case "string":
		return p.addObject(p.text())
	case "data":
		b, err := base64Decode([]byte(p.text()))
		if err != nil {
			return err
		}
		if b == nil {
			b = []byte{}
		}
		return p.addObject(b)
	case "date":
		t, err := parseDate(p.text())
		if err != nil {
			return err
		}
		return p.addObject(t)
	}
	return nil
}

// addObject mirrors _PlistParser.add_object.
func (p *parser) addObject(value any) error {
	if p.hasKey {
		d, ok := p.top().(*Dict)
		if !ok {
			return errors.New("unexpected element")
		}
		d.Set(p.currentKey, value)
		p.currentKey = ""
		p.hasKey = false
		return nil
	}
	if len(p.stack) == 0 {
		p.root = value
		return nil
	}
	a, ok := p.top().(*array)
	if !ok {
		return errors.New("unexpected element")
	}
	a.items = append(a.items, value)
	return nil
}

func (p *parser) top() any {
	if len(p.stack) == 0 {
		return nil
	}
	return p.stack[len(p.stack)-1]
}

func (p *parser) pop() {
	if len(p.stack) > 0 {
		p.stack = p.stack[:len(p.stack)-1]
	}
}

// freeze converts the parser's *array placeholders into []any, recursively.
func freeze(v any) any {
	switch x := v.(type) {
	case *Dict:
		for _, k := range x.Keys() {
			val, _ := x.Get(k)
			x.Set(k, freeze(val))
		}
		return x
	case *array:
		out := make([]any, len(x.items))
		for i, item := range x.items {
			out[i] = freeze(item)
		}
		return out
	}
	return v
}

var dateRe = regexp.MustCompile(`^(\d\d\d\d)(?:-(\d\d)(?:-(\d\d)(?:T(\d\d)(?::(\d\d)(?::(\d\d))?)?)?)?)?Z`)

// parseDate mirrors plistlib._date_from_string for naive datetimes. Python needs
// at least year, month and day, and it rejects out-of-range fields.
func parseDate(s string) (time.Time, error) {
	m := dateRe.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, fmt.Errorf("invalid date %q", s)
	}
	var fields []int
	for _, g := range m[1:] {
		if g == "" {
			break
		}
		n, _ := strconv.Atoi(g)
		fields = append(fields, n)
	}
	if len(fields) < 3 {
		return time.Time{}, errors.New("datetime requires year, month and day")
	}
	get := func(i int) int {
		if i < len(fields) {
			return fields[i]
		}
		return 0
	}
	year, month, day, hour, minute, second := get(0), get(1), get(2), get(3), get(4), get(5)
	if year < 1 || month < 1 || month > 12 || day < 1 || day > daysIn(month, year) ||
		hour > 23 || minute > 59 || second > 59 {
		return time.Time{}, fmt.Errorf("date out of range %q", s)
	}
	return time.Date(year, time.Month(month), day, hour, minute, second, 0, time.UTC), nil
}

func daysIn(month, year int) int {
	if month == 2 {
		if year%4 == 0 && (year%100 != 0 || year%400 == 0) {
			return 29
		}
		return 28
	}
	if month == 4 || month == 6 || month == 9 || month == 11 {
		return 30
	}
	return 31
}
