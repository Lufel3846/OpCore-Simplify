"""Reference oracle for go/internal/acpiguru/patches_a.go (ORIGINAL Scripts/acpi_guru.py).

Usage: python3 -I pyref_patches_a.py <repo_root> <fake_iasl>   (case list on stdin)

Each case: {"dir": tables dir or null, "dsl_dir": ..., "out": acpi_directory,
"compile": "" | "ok" | "fail", "calls": [{"fn": ..., ...}]}. The output is one
{"results": [{"ok": v, "error": bool}], "files": {name: hex}} per case.
"""

import copy
import json
import os
import re
import sys

repo = sys.argv[1]
fake_iasl = sys.argv[2]
sys.path.insert(0, repo)

from Scripts import acpi_guru as AG, dsdt as D, run, utils  # noqa: E402
from Scripts.datasets import acpi_patch_data  # noqa: E402

real_stdout = sys.stdout
sys.stdout = open(os.devnull, "w")


def make_dsdt():
    d = D.DSDT.__new__(D.DSDT)
    d.r = run.Run()
    d.u = utils.Utils()
    d.iasl = fake_iasl
    d.allowed_signatures = (b"APIC", b"DMAR", b"DSDT", b"SSDT")
    d.mixed_listing = (b"DSDT", b"SSDT")
    d.acpi_tables = {}
    d.hex_match = re.compile(r"^\s*[0-9A-F]{4,}:(\s[0-9A-F]{2})+(\s+\/\/.*)?$")
    d.type_match = re.compile(r".*(?P<type>Processor|Scope|Device|Method|Name) \((?P<name>[^,\)]+).*")
    return d


def fake_input(prompt="Press Enter to continue..."):
    raise EOFError()


def make_guru():
    g = AG.ACPIGuru.__new__(AG.ACPIGuru)
    g.acpi = make_dsdt()
    g.run = run.Run().run
    g.utils = utils.Utils()
    g.utils.head = lambda *a, **k: None
    g.utils.adjust_window_size = lambda *a, **k: None
    g.utils.request_input = fake_input
    g.patches = copy.deepcopy(acpi_patch_data.patches)
    g.hardware_report = None
    g.disabled_devices = None
    g.acpi_directory = None
    g.smbios_model = None
    g.dsdt = None
    g.dsdt_patches = []
    g.pre_patches = ()
    g.lpc_bus_device = None
    g.target_irqs = [0, 2, 8, 11]
    g.illegal_names = ("XHC1", "EHC1", "EHC2", "PXSX")
    return g


def conv(v):
    if isinstance(v, bytes):
        return {"hex": v.hex()}
    if isinstance(v, dict):
        return {"__dict__": [[k, conv(x)] for k, x in v.items()]}
    if isinstance(v, (list, tuple)):
        return [conv(x) for x in v]
    return v


def call(g, c):
    fn = c["fn"]
    if fn == "plug":
        return g.enable_cpu_power_management()
    if fn == "irq":
        return g.fix_irq_conflicts()
    if fn == "awac":
        return g.fix_system_clock_awac()
    if fn == "list_irqs":
        return g.list_irqs()
    if fn == "irq_choice":
        return g.get_irq_choice(g.list_irqs())
    if fn == "hex_irqs":
        return g.get_hex_from_irqs(c["irq"], c.get("rem"))
    if fn == "int_line":
        return g.get_int_for_line(c["irq"])
    if fn == "conv_irq":
        return g.convert_irq_to_int(c["irq"])
    if fn == "same_line":
        return g.same_line_irq(c["irq"])
    raise ValueError(fn)


out = []
for case in json.load(sys.stdin):
    g = make_guru()
    os.environ["FAKE_DSL_DIR"] = case.get("dsl_dir") or ""
    os.environ.pop("FAKE_COMPILE", None)
    if case.get("dir"):
        g.read_acpi_tables(case["dir"])
        g.dsdt = g.acpi.get_dsdt_or_only()
        try:
            g.lpc_bus_device = g.get_lpc_name()
        except Exception:  # noqa: BLE001
            g.lpc_bus_device = None
    g.acpi_directory = case["out"]
    if case.get("target_irqs") is not None:
        g.target_irqs = case["target_irqs"]
    if case.get("compile"):
        os.environ["FAKE_COMPILE"] = case["compile"]
    results = []
    for c in case["calls"]:
        try:
            results.append({"ok": conv(call(g, c)), "error": False})
        except Exception as e:  # noqa: BLE001
            results.append({"ok": None, "error": True, "message": type(e).__name__})
    files = {}
    if os.path.isdir(case["out"]):
        for f in sorted(os.listdir(case["out"])):
            files[f] = open(os.path.join(case["out"], f), "rb").read().hex()
    out.append({"results": results, "files": files})
real_stdout.write(json.dumps(out))
