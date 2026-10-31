// Command opcore-simplify is the Go command-line front end of the OpCore
// Simplify port. With no arguments it runs the interactive flow; the macos subcommand exposes the macos package.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/app"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/macos"
)

const usage = `usage:
  opcore-simplify                           interactive OpCore Simplify
  opcore-simplify macos latest [--stable]   newest Darwin version (default includes beta)
  opcore-simplify macos lowest              oldest supported Darwin version
  opcore-simplify macos name <darwin>       macOS release name for a Darwin version
  opcore-simplify macos parse <darwin>      validate and normalize a Darwin version
`

func main() {
	// No arguments starts the interactive flow of OpCore-Simplify.py.
	if len(os.Args) == 1 {
		os.Exit(app.Run())
	}
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes one command and returns the process exit code.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 || args[0] != "macos" {
		fmt.Fprint(stderr, usage)
		return 2
	}
	sub, rest := args[1], args[2:]
	switch sub {
	case "latest":
		fs := flag.NewFlagSet("macos latest", flag.ContinueOnError)
		fs.SetOutput(stderr)
		stable := fs.Bool("stable", false, "consider only final releases")
		if err := fs.Parse(rest); err != nil {
			return 2
		}
		fmt.Fprintln(stdout, macos.LatestDarwinVersion(!*stable))
	case "lowest":
		fmt.Fprintln(stdout, macos.LowestDarwinVersion())
	case "name", "parse":
		if len(rest) != 1 {
			fmt.Fprintf(stderr, "usage: opcore-simplify macos %s <darwin-version>\n", sub)
			return 2
		}
		if sub == "name" {
			name, err := macos.MacOSName(rest[0])
			if err != nil {
				fmt.Fprintln(stderr, "error:", err)
				return 1
			}
			fmt.Fprintln(stdout, name)
			return 0
		}
		v, err := macos.ParseVersion(rest[0])
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		fmt.Fprintln(stdout, v)
	default:
		fmt.Fprint(stderr, usage)
		return 2
	}
	return 0
}
