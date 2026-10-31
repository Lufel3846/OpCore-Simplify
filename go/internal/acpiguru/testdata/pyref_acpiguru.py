"""Reference oracle for go/internal/acpiguru, running the ORIGINAL Scripts/acpi_guru.py.

Usage: python3 -I pyref_acpiguru.py <repo_root> <fake_iasl>   (step list on stdin)

ACPIGuru.__init__ builds a DSDT, which needs a real iasl, so the instance is made
with __new__ and the attributes __init__ sets (the DSDT one points at the fake
iasl of the test). Steps share one instance. utils.request_input answers from the
step's "inputs" (EOFError when they run out) and the prompts are returned.

Each output item: {"ok": value, "error": bool, "prompts": [...]}. bytes become
{"hex": ...}; dicts become {"__dict__": [[key, value], ...]} to keep their order.
"""

import copy
import hashlib
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


state = {"inputs": [], "prompts": []}


def fake_input(self, prompt="Press Enter to continue..."):
    state["prompts"].append(prompt)
    if not state["inputs"]:
        raise EOFError()
    return state["inputs"].pop(0)


def make_guru():
    g = AG.ACPIGuru.__new__(AG.ACPIGuru)
    g.acpi = make_dsdt()
    g.run = run.Run().run
    g.utils = utils.Utils()
    g.utils.head = lambda *a, **k: None
    g.utils.adjust_window_size = lambda *a, **k: None
    g.utils.request_input = lambda prompt="Press Enter to continue...": fake_input(g.utils, prompt)
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


def names(g):
    return sorted(g.acpi.acpi_tables)


def table_state(g):
    t = g.dsdt
    return {
        "tables": names(g),
        "dsdt": None if t is None else hashlib.sha256(t["raw"]).hexdigest(),
    }


def step(g, s):
    op = s["op"]
    if op == "unique_name":
        return g.get_unique_name(s["name"], s["folder"], s.get("append", "-Patched"))
    if op == "unique_device":
        return list(g.get_unique_device("", s["base"], s.get("start", 0), s.get("used", [])))
    if op == "sorted_nicely":
        return g.sorted_nicely(s["list"])
    if op == "get_data":
        data = bytes.fromhex(s["bytes"]) if "bytes" in s else s["text"]
        return conv(g.get_data(data, s.get("pad_to", 0)))
    if op == "hex_to_bytes":
        return conv(g.utils.hex_to_bytes(s["value"]))
    if op == "apply_patches":
        return conv(g.apply_acpi_patches(s["patches"]))
    if op == "drop_table":
        sig = bytes.fromhex(s["sig"]) if s.get("sig") is not None else None
        oem = bytes.fromhex(s["oem"]) if s.get("oem") is not None else None
        return conv(g.dropping_the_table(sig, oem))
    if op == "drop_cpu":
        return conv(g.drop_cpu_tables())
    if op == "patch_index":
        return g.get_patch_index(s["name"])
    if op == "hedt":
        return g.is_intel_hedt_cpu(s["processor"], s["codename"])
    if op == "select_patches":
        g.select_acpi_patches(s["report"], s["disabled"])
        return [p.name for p in g.patches if p.checked]
    if op == "checked":
        return [p.name for p in g.patches if p.checked]
    if op == "customize":
        try:
            g.customize_patch_selection()
        except SystemExit:
            return {"exit": True, "checked": [p.name for p in g.patches if p.checked]}
        return {"exit": False, "checked": [p.name for p in g.patches if p.checked]}
    if op == "read_tables":
        r = g.read_acpi_tables(s["path"])
        return {"result": r, "state": table_state(g)}
    if op == "ensure":
        try:
            r = bool(g.ensure_dsdt(allow_any=s.get("allow_any", False)))
        except SystemExit:
            return {"exit": True, "state": table_state(g)}
        return {"result": r, "state": table_state(g), "exit": False}
    if op == "select_tables":
        try:
            r = g.select_acpi_tables()
        except SystemExit:
            return {"exit": True, "state": table_state(g)}
        return {"result": r, "state": table_state(g), "exit": False}
    if op == "set_dsdt":
        g.dsdt = g.acpi.get_dsdt_or_only()
        return True
    if op == "clear":
        g.dsdt = None
        g.acpi.acpi_tables = {}
        return True
    if op == "write_ssdt":
        g.acpi_directory = s["directory"]
        r = g.write_ssdt(s["name"], s["content"], s.get("compile", True))
        out = {"result": r}
        for ext in (".dsl", ".aml"):
            p = os.path.join(s["directory"], s["name"] + ext)
            out[ext] = open(p, "rb").read().hex() if os.path.exists(p) else None
        return out
    if op == "normalize_path":
        return g.utils.normalize_path(s["path"])
    raise ValueError(op)


results = []
g = make_guru()
for s in json.load(sys.stdin):
    state["inputs"] = list(s.get("inputs") or [])
    state["prompts"] = []
    try:
        results.append({"ok": step(g, s), "error": False, "prompts": state["prompts"]})
    except Exception as e:  # noqa: BLE001
        results.append({"ok": None, "error": True, "prompts": state["prompts"], "message": type(e).__name__})
real_stdout.write(json.dumps(results))
