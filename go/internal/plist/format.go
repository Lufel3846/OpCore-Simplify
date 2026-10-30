package plist

// FormatFloat returns Python's repr for f, the same text plistlib and json use
// for floats.
func FormatFloat(f float64) string {
	return pyFloatRepr(f)
}

// ParseInt mirrors Python's int(str, base) for base 10 or 16, returning an
// int64 or a *big.Int. Errors match Python's ValueError cases.
func ParseInt(s string, base int) (any, error) {
	return pyInt(s, base)
}
