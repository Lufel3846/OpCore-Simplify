"""Oracle for the group C ACPI patches of the ORIGINAL Scripts/acpi_guru.py.

Usage: python3 -I pyref_patches_c.py <repo_root> <fake_iasl>   (case list on stdin)

Each case: {method, dir, dsl, out, report (JSON text or null), model, mac, compile, lpc}.
dir holds the raw tables, dsl the disassembly the fake iasl serves (FAKE_DSL_DIR),
out the acpi_directory. Output per case: {ok, error, message, files{name: hex}, lpc}.
"""

import copy
import json
import os
import re
import sys

repo = sys.argv[1]
fake_iasl = sys.argv[2]
sys.path.insert(0, repo)

from Scripts import acpi_guru as AG, dsdt as D, run, smbios, utils  # noqa: E402
from Scripts.datasets import acpi_patch_data  # noqa: E402

real_stdout = sys.stdout
sys.stdout = open(os.devnull, "w")


ORIG_DSDT = D.DSDT


def make_dsdt():
    d = ORIG_DSDT.__new__(ORIG_DSDT)
    d.r = run.Run()
    d.u = utils.Utils()
    d.iasl = fake_iasl
    d.allowed_signatures = (b"APIC", b"DMAR", b"DSDT", b"SSDT")
    d.mixed_listing = (b"DSDT", b"SSDT")
    d.acpi_tables = {}
    d.hex_match = re.compile(r"^\s*[0-9A-F]{4,}:(\s[0-9A-F]{2})+(\s+\/\/.*)?$")
    d.type_match = re.compile(r".*(?P<type>Processor|Scope|Device|Method|Name) \((?P<name>[^,\)]+).*")
    return d


def fail_input(prompt="Press Enter to continue..."):
    raise EOFError()


def make_guru():
    g = AG.ACPIGuru.__new__(AG.ACPIGuru)
    g.acpi = make_dsdt()
    g.smbios = smbios.SMBIOS()
    g.run = run.Run().run
    g.utils = utils.Utils()
    g.utils.head = lambda *a, **k: None
    g.utils.adjust_window_size = lambda *a, **k: None
    g.utils.request_input = fail_input
    g.patches = copy.deepcopy(acpi_patch_data.patches)
    g.hardware_report = None
    g.disabled_devices = None
    g.acpi_directory = None
    g.smbios_model = None
    g.dsdt = None
    g.lpc_bus_device = None
    return g


def osi_strings():
    # __init__ builds the real DSDT (needs iasl); take the dict from a stub instance.
    orig = D.DSDT
    D.DSDT = lambda *a, **k: make_dsdt()
    try:
        return AG.ACPIGuru().osi_strings
    finally:
        D.DSDT = orig


OSI = osi_strings()


def conv(v):
    if isinstance(v, bytes):
        return {"hex": v.hex()}
    if isinstance(v, dict):
        return {"__dict__": [[k, conv(x)] for k, x in v.items()]}
    if isinstance(v, (list, tuple)):
        return [conv(x) for x in v]
    return v


def run_case(c):
    g = make_guru()
    g.osi_strings = OSI
    os.environ.pop("FAKE_COMPILE", None)
    os.environ["FAKE_DSL_DIR"] = c["dsl"]
    g.read_acpi_tables(c["dir"])
    g.dsdt = g.acpi.get_dsdt_or_only()
    g.hardware_report = json.loads(c["report"]) if c.get("report") is not None else None
    g.acpi_directory = c["out"]
    g.smbios_model = c.get("model", "")
    if c.get("lpc", True):
        g.lpc_bus_device = g.get_lpc_name()
    mac = c.get("mac", "AABBCCDDEEFF")
    g.smbios.generate_random_mac = lambda: mac
    if c.get("compile"):
        os.environ["FAKE_COMPILE"] = c["compile"]
    out = {"ok": None, "error": False, "message": "", "files": {}, "lpc": g.lpc_bus_device}
    try:
        out["ok"] = conv(getattr(g, c["method"])())
    except Exception as e:  # noqa: BLE001
        out["error"] = True
        out["message"] = type(e).__name__
    if os.path.isdir(c["out"]):
        for n in sorted(os.listdir(c["out"])):
            out["files"][n] = open(os.path.join(c["out"], n), "rb").read().hex()
    return out


results = [run_case(c) for c in json.load(sys.stdin)]
real_stdout.write(json.dumps(results))
