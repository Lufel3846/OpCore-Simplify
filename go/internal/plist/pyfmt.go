// Package plist is the Go port of the plist handling of OpCore Simplify, which
// relies on Python's plistlib (utils.read_file / utils.write_file, kext
// Info.plist editing, and the OpenCore config.plist). It reproduces plistlib's
// XML reader and writer, including the exact output bytes.
//
// OpCore Simplify is distributed under the BSD 3-Clause License
// (Copyright (c) 2024, lzhoang2801). See go/LICENSE.
package plist

import (
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// pyFloatRepr mirrors Python's repr(float): shortest round-trip digits, fixed
// notation for decimal exponents in [-4, 16), otherwise scientific notation
// with a sign and at least two exponent digits.
func pyFloatRepr(f float64) string {
	switch {
	case math.IsNaN(f):
		return "nan"
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	}
	s := strconv.FormatFloat(f, 'e', -1, 64)
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign = "-"
		s = s[1:]
	}
	mant, expText, _ := strings.Cut(s, "e")
	exp, _ := strconv.Atoi(expText)
	digits := strings.Replace(mant, ".", "", 1)

	if exp >= -4 && exp < 16 {
		if exp < 0 {
			return sign + "0." + strings.Repeat("0", -exp-1) + digits
		}
		if len(digits) <= exp+1 {
			return sign + digits + strings.Repeat("0", exp+1-len(digits)) + ".0"
		}
		return sign + digits[:exp+1] + "." + digits[exp+1:]
	}
	out := digits[:1]
	if len(digits) > 1 {
		out += "." + digits[1:]
	}
	return fmt.Sprintf("%s%se%+03d", sign, out, exp)
}

// pyFloat mirrors Python's float(str) for decimal input: surrounding whitespace
// is ignored, underscores are allowed between digits, and hexadecimal is not
// accepted. Values outside the float64 range become ±Inf, as in Python.
func pyFloat(s string) (float64, error) {
	t := strings.TrimSpace(s)
	if strings.ContainsAny(t, "xX") {
		return 0, fmt.Errorf("could not convert string to float: %q", s)
	}
	if strings.Contains(t, "_") {
		if !validUnderscores(t) {
			return 0, fmt.Errorf("could not convert string to float: %q", s)
		}
		t = strings.ReplaceAll(t, "_", "")
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return 0, fmt.Errorf("could not convert string to float: %q", s)
	}
	return f, nil
}

// validUnderscores reports whether every '_' sits between two ASCII digits.
func validUnderscores(t string) bool {
	for i := 0; i < len(t); i++ {
		if t[i] != '_' {
			continue
		}
		if i == 0 || i == len(t)-1 || !isDigit(t[i-1]) || !isDigit(t[i+1]) {
			return false
		}
	}
	return true
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// pyInt mirrors Python's int(str, base) for base 10 or 16: whitespace is
// ignored, a sign is allowed, and single underscores may separate digits.
// The result is an int64 when it fits and a *big.Int otherwise, so there is
// no overflow, just like Python's int.
func pyInt(s string, base int) (any, error) {
	t := strings.TrimSpace(s)
	neg := false
	if strings.HasPrefix(t, "+") {
		t = t[1:]
	} else if strings.HasPrefix(t, "-") {
		neg = true
		t = t[1:]
	}
	if base == 16 && (strings.HasPrefix(t, "0x") || strings.HasPrefix(t, "0X")) {
		t = t[2:]
	}
	if t == "" || t[0] == '_' || t[len(t)-1] == '_' || strings.Contains(t, "__") {
		return nil, fmt.Errorf("invalid literal for int() with base %d: %q", base, s)
	}
	digits := strings.ReplaceAll(t, "_", "")
	for i := 0; i < len(digits); i++ {
		c := digits[i]
		ok := isDigit(c) || (base == 16 && ((c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')))
		if !ok {
			return nil, fmt.Errorf("invalid literal for int() with base %d: %q", base, s)
		}
	}
	n, ok := new(big.Int).SetString(digits, base)
	if !ok {
		return nil, fmt.Errorf("invalid literal for int() with base %d: %q", base, s)
	}
	if neg {
		n.Neg(n)
	}
	if n.IsInt64() {
		return n.Int64(), nil
	}
	return n, nil
}

// base64Decode mirrors binascii.a2b_base64 in its default (non-strict) mode:
// characters outside the alphabet are skipped, and decoding stops at padding
// once enough of it has been seen.
func base64Decode(in []byte) ([]byte, error) {
	var out []byte
	var quad [4]byte
	quadPos, pads := 0, 0
	for _, c := range in {
		if c == '=' {
			if quadPos >= 2 {
				pads++
				if quadPos+pads >= 4 {
					break
				}
			}
			continue
		}
		v := base64Value(c)
		if v < 0 {
			continue
		}
		quad[quadPos] = byte(v)
		quadPos++
		if quadPos == 4 {
			out = append(out, quad[0]<<2|quad[1]>>4, quad[1]<<4|quad[2]>>2, quad[2]<<6|quad[3])
			quadPos = 0
		}
	}
	switch quadPos {
	case 0:
		return out, nil
	case 1:
		return nil, errors.New("invalid base64-encoded string: number of data characters cannot be 1 more than a multiple of 4")
	}
	if quadPos+pads < 4 {
		return nil, errors.New("incorrect padding")
	}
	// Padding ended the input: emit the partial quad (2 or 3 characters).
	if quadPos == 2 {
		out = append(out, quad[0]<<2|quad[1]>>4)
	} else {
		out = append(out, quad[0]<<2|quad[1]>>4, quad[1]<<4|quad[2]>>2)
	}
	return out, nil
}

func base64Value(c byte) int {
	switch {
	case c >= 'A' && c <= 'Z':
		return int(c - 'A')
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 26
	case c >= '0' && c <= '9':
		return int(c-'0') + 52
	case c == '+':
		return 62
	case c == '/':
		return 63
	}
	return -1
}

// base64Encode mirrors plistlib._encode_base64: the input is split into chunks
// of (maxLineLength//4)*3 bytes and each chunk is encoded on its own line.
func base64Encode(data []byte, maxLineLength int) []string {
	maxBin := (maxLineLength / 4) * 3
	var lines []string
	for i := 0; i < len(data); i += maxBin {
		end := min(i+maxBin, len(data))
		lines = append(lines, base64.StdEncoding.EncodeToString(data[i:end]))
	}
	return lines
}
