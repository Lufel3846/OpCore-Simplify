// Package app ports OpCore-Simplify.py (class OCPE and the __main__ loop) and
// the small helpers of Scripts/utils.py that the main flow uses.
//
// The self-updater (updater.py) is not ported.
//
// OpCore Simplify is distributed under the BSD 3-Clause License
// (Copyright (c) 2024, lzhoang2801). See go/LICENSE.
package app

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/acpiguru"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/compat"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/kextmaestro"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/macos"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/smbios"
)

// ErrExit stands for sys.exit(0) in utils.exit_program.
var ErrExit = errors.New("exit program")

// isExit reports whether err is one of the package exit sentinels. Python's
// SystemExit is not an Exception, so the main loop does not catch it.
func isExit(err error) bool {
	return errors.Is(err, ErrExit) || errors.Is(err, acpiguru.ErrExit) ||
		errors.Is(err, kextmaestro.ErrExit) || errors.Is(err, compat.ErrExit) ||
		errors.Is(err, smbios.ErrExit)
}

// Utils holds the terminal helpers of utils.Utils.
type Utils struct {
	ScriptName string
	In         *bufio.Reader
	Out        io.Writer
}

// NewUtils returns Utils bound to stdin and stdout.
func NewUtils(scriptName string) *Utils {
	return &Utils{ScriptName: scriptName, In: bufio.NewReader(os.Stdin), Out: os.Stdout}
}

func (u *Utils) printf(format string, a ...any) { fmt.Fprintf(u.Out, format, a...) }

// CleanTemporaryDir mirrors clean_temporary_dir.
func (u *Utils) CleanTemporaryDir() {
	tmp := os.TempDir()
	entries, err := os.ReadDir(tmp)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "ocs_") {
			continue
		}
		p := filepath.Join(tmp, e.Name())
		if st, err := os.Stat(p); err != nil || !st.IsDir() {
			continue
		}
		_ = os.RemoveAll(p)
	}
}

// RequestInput mirrors request_input: print the prompt and read one line. EOF
// without data is Python's EOFError.
func (u *Utils) RequestInput(prompt string) (string, error) {
	fmt.Fprint(u.Out, prompt)
	line, err := u.In.ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("EOFError: EOF when reading a line")
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// Pause is request_input() with the default prompt.
func (u *Utils) Pause() error {
	_, err := u.RequestInput("Press Enter to continue...")
	return err
}

// center is str.center.
func center(s string, width int) string {
	pad := width - len([]rune(s))
	if pad <= 0 {
		return s
	}
	left := pad/2 + (pad & width & 1)
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", pad-left)
}

func padRight(s string, width int) string {
	if n := width - len([]rune(s)); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// Head mirrors head(text, 68, resize=True). An empty text uses the script name
// (head(None)).
func (u *Utils) Head(text string) {
	const width = 68
	u.printf("\033[8;%d;%dt\n", 30, 100) // adjust_window_size("")
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", "cls")
	} else {
		cmd = exec.Command("clear")
	}
	cmd.Stdout = u.Out
	_ = cmd.Run()
	if text == "" {
		text = u.ScriptName
	}
	sep := strings.Repeat("═", width-2)
	title := " " + text + " "
	if r := []rune(title); len(r) > width-2 {
		title = string(r[:width-4]) + "..."
	}
	title = center(title, width-2)
	u.printf("╔%s╗\n║%s║\n╚%s╝\n", sep, title, sep)
}

// ProgressBar mirrors progress_bar.
func (u *Utils) ProgressBar(title string, steps []string, current int, done bool) {
	u.Head(title)
	u.printf("\n")
	if done {
		for _, s := range steps {
			u.printf("  [\033[92m✓\033[0m] %s\n", s)
		}
	} else {
		for i, s := range steps {
			switch {
			case i < current:
				u.printf("  [\033[92m✓\033[0m] %s\n", s)
			case i == current:
				u.printf("  [\033[1;93m>\033[0m] %s...\n", s)
			default:
				u.printf("  [ ] %s\n", s)
			}
		}
	}
	u.printf("\n")
}

// OpenFolder mirrors open_folder. Like subprocess.run, a command that cannot be
// started is an error, a non-zero exit status is not.
func (u *Utils) OpenFolder(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", path)
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil
		}
		return err
	}
	return nil
}

// ExitProgram mirrors exit_program: farewell screen, wait for Enter, exit.
func (u *Utils) ExitProgram() error {
	const width = 68
	u.Head("")
	u.printf("\n%s\n\n", center("For more information, to report errors, or to contribute to the product:", width))
	sep := strings.Repeat("─", width-4)
	u.printf(" ┌%s┐ \n", sep)
	for _, c := range [][2]string{
		{"Facebook", "https://www.facebook.com/macforce2601"},
		{"Telegram", "https://t.me/lzhoang2801"},
		{"GitHub", "https://github.com/lzhoang2801/OpCore-Simplify"},
	} {
		line := " * " + c[0] + ": " + c[1]
		u.printf(" │%s│ \n", padRight(line, width-4))
	}
	u.printf(" └%s┘ \n\n%s\n\n", sep, center("Thank you for using our program!", width))
	if _, err := u.RequestInput(center("Press Enter to exit.", width)); err != nil {
		return err
	}
	return ErrExit
}

// NormalizePath mirrors normalize_path.
func (u *Utils) NormalizePath(p string) (string, error) { return acpiguru.NormalizePath(p) }

// ParseDarwinVersion mirrors parse_darwin_version.
func (u *Utils) ParseDarwinVersion(v string) (macos.Version, error) { return macos.ParseVersion(v) }

// runCommand mirrors Run.run for one command: output is streamed to the
// terminal and collected. A command that cannot start gives
// ("", "Command not found!", 1).
func runCommand(args []string) (string, string, int) {
	if len(args) == 0 {
		return "", "Command not found!", 1
	}
	var so, se strings.Builder
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout = io.MultiWriter(os.Stdout, &so)
	cmd.Stderr = io.MultiWriter(os.Stderr, &se)
	if err := cmd.Start(); err != nil {
		return "", "Command not found!", 1
	}
	_ = cmd.Wait()
	return so.String(), se.String(), cmd.ProcessState.ExitCode()
}
