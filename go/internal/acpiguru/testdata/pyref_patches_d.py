"""Oracle for the group D ACPI patches of go/internal/acpiguru, running the ORIGINAL Scripts/acpi_guru.py.

Usage: python3 -I pyref_patches_d.py <repo_root> <fake_iasl>   (step list on stdin)

Steps: {"op":"setup","path","out","lpc"} loads the tables (read_acpi_tables), sets
dsdt, acpi_directory, lpc_bus_device. {"op":"call","name",...} calls a method;
{"op":"text","fn","args"} calls a text helper; {"op":"run_all","checked"} runs the
build_opencore_efi loop. Every step returns the value and the files in out.
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


def make_guru():
    g = AG.ACPIGuru.__new__(AG.ACPIGuru)
    g.acpi = make_dsdt()
    g.run = run.Run().run
    g.utils = utils.Utils()
    g.utils.head = lambda *a, **k: None
    g.utils.adjust_window_size = lambda *a, **k: None
    g.utils.request_input = lambda prompt="": (_ for _ in ()).throw(EOFError())
    g.patches = copy.deepcopy(acpi_patch_data.patches)
    g.hardware_report = None
    g.disabled_devices = None
    g.acpi_directory = None
    g.smbios_model = None
    g.dsdt = None
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


def files(out):
    r = {}
    if os.path.isdir(out):
        for n in sorted(os.listdir(out)):
            r[n] = open(os.path.join(out, n), "rb").read().hex()
    return r


def wipe(out):
    if os.path.isdir(out):
        for n in os.listdir(out):
            os.remove(os.path.join(out, n))


def run_all(g, checked):
    for p in g.patches:
        p.checked = p.name in checked
    cfg = {"Add": [], "Delete": [], "Patch": []}
    ecen = None
    for patch in g.patches:
        if patch.checked:
            if patch.name == "BATP":
                patch.checked = getattr(g, patch.function_name)()
                ecen = patch.checked
                continue
            acpi_load = getattr(g, patch.function_name)()
            if not isinstance(acpi_load, dict):
                continue
            cfg["Add"].extend(acpi_load.get("Add", []))
            cfg["Delete"].extend(acpi_load.get("Delete", []))
            cfg["Patch"].extend(acpi_load.get("Patch", []))
    cfg["Patch"].extend(g.dsdt_patches)
    cfg["Patch"] = g.apply_acpi_patches(cfg["Patch"])
    return {"cfg": cfg, "ecen": ecen}


g = make_guru()
cur_out = [None]


def step(s):
    op = s["op"]
    if op == "setup":
        g.acpi.acpi_tables = {}
        g.dsdt = None
        g.read_acpi_tables(s["path"])
        g.dsdt = g.acpi.get_dsdt_or_only()
        g.hardware_report = s.get("report")
        g.acpi_directory = s["out"]
        os.makedirs(s["out"], exist_ok=True)
        cur_out[0] = s["out"]
        g.lpc_bus_device = g.get_lpc_name() if s.get("lpc", True) else None
        g.dsdt_patches = s.get("dsdt_patches", [])
        return {"dsdt": g.dsdt is not None, "lpc": g.lpc_bus_device, "tables": sorted(g.acpi.acpi_tables)}
    wipe(cur_out[0])
    if op == "call":
        r = getattr(g, s["name"])()
    elif op == "text":
        r = getattr(g, s["fn"])(*s["args"])
    elif op == "run_all":
        r = run_all(g, s["checked"])
    elif op == "no_dsdt":
        saved = g.dsdt
        g.dsdt = None
        r = g.battery_status_patch()
        g.dsdt = saved
    elif op == "set_table":
        saved = g.dsdt
        g.dsdt = {"table": s["table"]}
        try:
            r = g.battery_status_patch()
        finally:
            g.dsdt = saved
    else:
        raise ValueError(op)
    return {"value": conv(r), "files": files(cur_out[0])}


results = []
for s in json.load(sys.stdin):
    try:
        results.append({"ok": step(s), "error": False})
    except Exception as e:  # noqa: BLE001
        results.append({"ok": None, "error": True, "message": type(e).__name__, "files": files(cur_out[0]) if cur_out[0] else {}})
real_stdout.write(json.dumps(results))
