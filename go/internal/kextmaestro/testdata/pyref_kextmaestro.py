"""Reference oracle for go/internal/kextmaestro, using the ORIGINAL kext_maestro.py.

Usage: python3 -I pyref_kextmaestro.py <repo_root>   (one JSON request list on stdin)

Prompts are answered by answer(), which the Go test mirrors exactly. Terminal
output (utils.head and print) is discarded.

Each success is {"ok": json.dumps(value, indent=4)}; errors are {"error": <ExceptionName>}.
Ops (each request has "op"):
  reset                       -> restore every kext to its constructor state
  set_checked {names}         -> checked = True for each name
  select {report, macos, needs_oclp, acpi}
  check {name, target, force} -> check_kext
  uncheck {name}              -> uncheck_kext
  status {name, target}       -> get_kext_status
  compat {name, target}       -> is_kext_compatible
  verify {names, target}      -> sorted names forced by verify_kext_compatibility
  pci {path}                  -> extract_pci_id
  process {dir, path}        -> process_kext
  install {macos, dir, ock}   -> install_kexts_to_efi, then the tree of dir
  load {report, macos, dir}   -> load_kexts
  modify {path, report, macos}-> modify_kexts, then the sha256 of the file
  checked                     -> sorted names of checked kexts
"""

import hashlib
import json
import os
import sys

repo = sys.argv[1]
sys.path.insert(0, repo)

from Scripts import kext_maestro, utils  # noqa: E402
from Scripts.datasets import kext_data  # noqa: E402


def answer(prompt):
    if "audio kext" in prompt:
        return "2"
    if "AMD" in prompt and "GPU" in prompt:
        return "1"
    if "Intel Wi-Fi" in prompt:
        return "2"
    if "force load" in prompt:
        return "yes"
    return ""


utils.Utils.request_input = lambda self, prompt="Press Enter to continue...": answer(prompt)
utils.Utils.head = lambda self, *a, **k: None
real_stdout = sys.stdout
sys.stdout = open(os.devnull, "w")

km = kext_maestro.KextMaestro()


def index(name):
    return kext_data.kext_index_by_name.get(name)


def tree(root):
    out = {}
    if not os.path.isdir(root):
        return out
    for dirpath, _, files in os.walk(root):
        for name in files:
            full = os.path.join(dirpath, name)
            with open(full, "rb") as f:
                out[os.path.relpath(full, root).replace("\\", "/")] = hashlib.sha256(f.read()).hexdigest()
    return dict(sorted(out.items()))


def checked_names():
    return sorted(k.name for k in km.kexts if k.checked)


def run(req):
    result = _run(req)
    if "ok" in result:
        result["ok"] = json.dumps(result["ok"], indent=4)
    return result


def _run(req):
    op = req["op"]
    try:
        if op == "reset":
            for k in km.kexts:
                k.checked = k.required
            return {"ok": None}
        if op == "set_checked":
            for name in req["names"]:
                km.kexts[index(name)].checked = True
            return {"ok": checked_names()}
        if op == "select":
            patches = [type("Patch", (), {"name": p["name"], "checked": p["checked"]})() for p in req["acpi"]]
            needs = km.select_required_kexts(req["report"], req["macos"], req["needs_oclp"], patches)
            return {"ok": {"needs_oclp": needs, "checked": checked_names()}}
        if op == "check":
            return {"ok": km.check_kext(index(req["name"]), req["target"], req["force"])}
        if op == "uncheck":
            return {"ok": km.uncheck_kext(index(req["name"]))}
        if op == "status":
            return {"ok": km.get_kext_status(km.kexts[index(req["name"])], req["target"])}
        if op == "compat":
            return {"ok": km.is_kext_compatible(km.kexts[index(req["name"])], req["target"])}
        if op == "verify":
            return {"ok": sorted(km.verify_kext_compatibility(req["names"], req["target"]))}
        if op == "pci":
            return {"ok": km.extract_pci_id(req["path"])}
        if op == "process":
            return {"ok": km.process_kext(req["dir"], req["path"])}
        if op == "install":
            km.ock_files_dir = req["ock"]
            km.install_kexts_to_efi(req["macos"], req["dir"])
            return {"ok": tree(req["dir"])}
        if op == "load":
            return {"ok": km.load_kexts(req["report"], req["macos"], req["dir"])}
        if op == "modify":
            km.modify_kexts(req["path"], req["report"], req["macos"])
            with open(req["path"], "rb") as f:
                return {"ok": hashlib.sha256(f.read()).hexdigest()}
        if op == "checked":
            return {"ok": checked_names()}
        raise ValueError("unknown op " + op)
    except Exception as exc:
        return {"error": type(exc).__name__}


results = [run(r) for r in json.load(sys.stdin)]
sys.stdout = real_stdout
json.dump(results, sys.stdout)
