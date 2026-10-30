package plist

// FormatFloat returns Python's repr for f, the same text plistlib and json use
// for floats.
func FormatFloat(f float64) string {
	return pyFloatRepr(f)
}
