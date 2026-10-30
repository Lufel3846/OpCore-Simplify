"""Reference oracle for go/internal/dsdt, running the ORIGINAL Scripts/dsdt.py.

Usage: python3 -I pyref_dsdt.py <repo_root> <fake_iasl>   (step list on stdin)

The original DSDT.__init__ needs a real iasl, so the instance is built with
__new__ and the same attributes __init__ sets, with `iasl` pointing at the fake
iasl script of the test. The steps share one instance, as acpi_guru does.

Each step is {"op": ..., ...}; the output is one {"ok": value, "error": bool}
per step. Tuples are emitted as lists.
"""

import hashlib
import json
import os
import re
import sys

repo = sys.argv[1]
fake_iasl = sys.argv[2]
sys.path.insert(0, repo)

from Scripts import dsdt as D, run, utils  # noqa: E402

real_stdout = sys.stdout
sys.stdout = open(os.devnull, "w")


def make():
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


def sha(s):
    return hashlib.sha256(s.encode("utf-8")).hexdigest()


def detail(t):
    out = {
        "signature": t["signature"].hex(),
        "oem": t["oem"].hex(),
        "id": t["id"].hex(),
        "revision": t["revision"],
        "oem_revision": str(t["oem_revision"]),
        "length": t["length"],
        "raw": hashlib.sha256(t["raw"]).hexdigest(),
        "table": sha(t["table"]),
        "lines": sha("\n".join(t["lines"])),
        "nlines": len(t["lines"]),
        "scopes": [list(x) for x in t["scopes"]],
        "paths": [list(x) for x in t["paths"]],
    }
    for k in ("signature_ascii", "oem_ascii", "id_ascii"):
        out[k] = t.get(k)
    return out


def pick(d, name):
    if name is None:
        return None
    return d.acpi_tables[name]


def step(d, s):
    op = s["op"]
    t = pick(d, s.get("table"))
    if op == "load":
        loaded, failed = d.load(s["path"])
        return {
            "loaded": {k: detail(v) for k, v in loaded.items()},
            "failed": sorted(failed),
            "all": sorted(d.acpi_tables),
        }
    if op == "reset":
        d.acpi_tables = {}
        return True
    if op == "hex_from_int":
        return d.get_hex_from_int(s["total"], s.get("pad_to", 4))
    if op == "ascii":
        u, a = d.get_ascii_print(bytes(s["data"]))
        return [u, a]
    if op == "valid":
        return d.table_is_valid(s["path"], s.get("name"))
    if op == "signature":
        r = d._table_signature(s["path"], s.get("name"))
        return None if r is None else r.hex()
    if op == "dsdt_or_only":
        r = d.get_dsdt_or_only()
        return None if r is None else r["raw"].hex()[:64] + ":" + str(len(r["raw"]))
    if op == "table_with_id":
        r = d.get_table_with_id(bytes.fromhex(s["value"]))
        return None if r is None else hashlib.sha256(r["raw"]).hexdigest()
    if op == "table_with_sig":
        r = d.get_table_with_signature(bytes.fromhex(s["value"]))
        return None if r is None else hashlib.sha256(r["raw"]).hexdigest()
    if op == "table":
        r = d.get_table(bytes.fromhex(s["value"]))
        return None if r is None else hashlib.sha256(r["raw"]).hexdigest()
    if op == "is_hex":
        return d.is_hex(s["line"])
    if op == "get_hex":
        return d.get_hex(s["line"])
    if op == "get_line":
        return d.get_line(s["line"])
    if op == "prev_hex":
        return list(d.find_previous_hex(s["index"], table=t))
    if op == "next_hex":
        return list(d.find_next_hex(s["index"], table=t))
    if op == "hex_start":
        return list(d.get_hex_starting_at(s["index"], table=t))
    if op == "hex_end":
        return list(d.get_hex_ending_at(s["index"], table=t))
    if op == "unique_pad":
        direction = {"both": None, "back": False, "fwd": True}[s["direction"]]
        return list(d.get_unique_pad(s["hex"], s["index"], direction, s.get("instance", 0), table=t))
    if op == "shortest_pad":
        r = d.get_shortest_unique_pad(s["hex"], s["index"], s.get("instance", 0), table=t)
        return None if r is None else list(r)
    if op == "devices":
        kw = {}
        if s.get("types") is not None:
            kw["types"] = tuple(s["types"])
        return [list(x) for x in d.get_devices(s["search"], strip_comments=s.get("strip", False), table=t, **kw)]
    if op == "scope":
        return d.get_scope(s["index"], add_hex=s.get("add_hex", False), strip_comments=s.get("strip", False), table=t)
    if op == "scopes":
        return [list(x) for x in d.get_scopes(table=t)]
    if op == "paths":
        return [list(x) for x in d.get_paths(table=t)]
    if op == "path_of_type":
        return [list(x) for x in d.get_path_of_type(s["type"], s["obj"], table=t)]
    if op == "device_paths":
        return [list(x) for x in d.get_device_paths(s["obj"], table=t)]
    if op == "method_paths":
        return [list(x) for x in d.get_method_paths(s["obj"], table=t)]
    if op == "name_paths":
        return [list(x) for x in d.get_name_paths(s["obj"], table=t)]
    if op == "processor_paths":
        return [list(x) for x in d.get_processor_paths(s["type"], table=t)]
    if op == "hid_paths":
        return [list(x) for x in d.get_device_paths_with_hid(s["hid"], table=t)]
    raise ValueError(op)


results = []
d = make()
for s in json.load(sys.stdin):
    try:
        results.append({"ok": step(d, s), "error": False})
    except Exception as e:  # noqa: BLE001
        results.append({"ok": None, "error": True, "message": type(e).__name__})
real_stdout.write(json.dumps(results))
