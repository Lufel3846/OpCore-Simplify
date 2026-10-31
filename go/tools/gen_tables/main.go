// Temporary helper: writes synthetic ACPI tables and their fake disassembly for
// the Beta side-by-side runs. Usage: go run ./tools/gen_tables <out-dir>
package main

import (
	"os"
	"path/filepath"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/dsdt/dsdttest"
)

func main() {
	out := os.Args[1]
	_ = os.MkdirAll(filepath.Join(out, "acpi"), 0o755)
	_ = os.MkdirAll(filepath.Join(out, "dsl"), 0o755)
	t := dsdttest.BuildTable(dsdttest.TableOpts{Sig: "DSDT", OEM: "ALASKA", ID: "A M I   ", BodySize: 600, Seed: 1})
	_ = os.WriteFile(filepath.Join(out, "acpi", "DSDT.aml"), t.Raw, 0o644)
	_ = os.WriteFile(filepath.Join(out, "dsl", "DSDT.aml.dsl"), []byte(t.DSL), 0o644)
	_ = os.WriteFile(filepath.Join(out, "iasl"), []byte(dsdttest.FakeIasl), 0o755)
}
