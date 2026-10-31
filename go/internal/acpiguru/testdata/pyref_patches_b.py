"""Oracle for group B patches (patches_b.go), running the ORIGINAL Scripts/acpi_guru.py.

Usage: python3 -I pyref_patches_b.py <repo_root> <fake_iasl>   (case list on stdin)

Each case: {method, dir, dsl_dir, out, report, disabled, smbios_model, lpc, args}.
A fresh ACPIGuru is built per case, the tables are loaded with the real
read_acpi_tables, the patch method is called and every file written to `out`
is returned (hex) next to the result and the (possibly mutated) disabled devices.
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
os.environ["FAKE_COMPILE"] = "ok"


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


def make_guru():
    g = AG.ACPIGuru.__new__(AG.ACPIGuru)
    g.acpi = make_dsdt()
    g.run = run.Run().run
    g.utils = utils.Utils()
    g.utils.head = lambda *a, **k: None
    g.utils.adjust_window_size = lambda *a, **k: None
    g.utils.request_input = lambda prompt="": ""
    g.patches = copy.deepcopy(acpi_patch_data.patches)
    g.hardware_report = None
    g.disabled_devices = None
    g.acpi_directory = None
    g.smbios_model = None
    g.dsdt = None
    g.lpc_bus_device = None
    g.dsdt_patches = []
    g.pre_patches = ()
    return g


def conv(v):
    if isinstance(v, bytes):
        return {"hex": v.hex()}
    if isinstance(v, dict):
        return {"__dict__": [[k, conv(x)] for k, x in v.items()]}
    if isinstance(v, (list, tuple)):
        return [conv(x) for x in v]
    return v


def unconv(v):
    """JSON dicts keep their order in python already; nothing to do."""
    return v


def run_case(c):
    os.environ["FAKE_DSL_DIR"] = c["dsl_dir"]
    g = make_guru()
    g.acpi_directory = c["out"]
    g.hardware_report = c.get("report")
    g.disabled_devices = c.get("disabled")
    g.smbios_model = c.get("smbios_model")
    g.read_acpi_tables(c["dir"])
    g.dsdt = g.acpi.get_dsdt_or_only()
    g.lpc_bus_device = g.get_lpc_name() if c.get("lpc", True) else c.get("lpc_value")
    out = {"ok": None, "error": False, "message": "", "files": {}, "lpc": g.lpc_bus_device,
           "disabled": None}
    try:
        m = c["method"]
        if m == "findall":
            out["ok"] = conv(g.findall_power_resource_blocks(c["lines"]))
        elif m == "is_method":
            out["ok"] = g.is_method_in_power_resource(tuple(c["method_path"]), c["lines"])
        else:
            out["ok"] = conv(getattr(g, m)())
    except Exception as e:  # noqa: BLE001
        out["error"] = True
        out["message"] = type(e).__name__
    out["disabled"] = conv(g.disabled_devices)
    if os.path.isdir(c["out"]):
        for n in sorted(os.listdir(c["out"])):
            out["files"][n] = open(os.path.join(c["out"], n), "rb").read().hex()
    return out


results = [run_case(c) for c in json.load(sys.stdin)]
real_stdout.write(json.dumps(results))
