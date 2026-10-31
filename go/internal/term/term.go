// Package term holds the terminal helpers shared by every screen: the title
// box of Utils.head, adjust_window_size and str.center.
package term

import (
	"fmt"
	"io"
	"strings"
)

// DefaultScriptName is Utils.script_name.
const DefaultScriptName = "OpCore Simplify"

// Center is str.center.
func Center(s string, width int) string {
	pad := width - len([]rune(s))
	if pad <= 0 {
		return s
	}
	left := pad/2 + (pad & width & 1)
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", pad-left)
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		switch rs[i] {
		case '\n', '\v', '\f', '\x1c', '\x1d', '\x1e', '\u0085', ' ', ' ', '\r':
			lines = append(lines, string(rs[start:i]))
			if rs[i] == '\r' && i+1 < len(rs) && rs[i+1] == '\n' {
				i++
			}
			start = i + 1
		}
	}
	if start < len(rs) {
		lines = append(lines, string(rs[start:]))
	}
	return lines
}

// AdjustWindowSize is Utils.adjust_window_size(content).
func AdjustWindowSize(w io.Writer, content string) {
	if w == nil {
		return
	}
	lines := splitLines(content)
	cols := 0
	for _, l := range lines {
		if n := len([]rune(l)); n > cols {
			cols = n
		}
	}
	rows := max(len(lines)+6, 30)
	cols = max(cols+2, 100)
	fmt.Fprintf(w, "\033[8;%d;%dt\n", rows, cols)
}

// Head is Utils.head(text, width, resize).
func Head(w io.Writer, text string, width int, resize bool) {
	if w == nil {
		return
	}
	if resize {
		AdjustWindowSize(w, "")
	}
	io.WriteString(w, "\033[H\033[2J\033[3J")
	sep := strings.Repeat("═", width-2)
	title := " " + text + " "
	if r := []rune(title); len(r) > width-2 {
		title = string(r[:width-4]) + "..."
	}
	title = Center(title, width-2)
	fmt.Fprintf(w, "╔%s╗\n║%s║\n╚%s╝\n", sep, title, sep)
}

// HeadDefault is Utils.head() with text None.
func HeadDefault(w io.Writer, scriptName string) {
	if scriptName == "" {
		scriptName = DefaultScriptName
	}
	Head(w, scriptName, 68, true)
}
