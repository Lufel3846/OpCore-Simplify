"""Reference oracle for go/internal/gathering, running the ORIGINAL gathering_files.py.

Usage: python3 -I pyref_gathering.py <repo_root>  (request on stdin, one step per process)
Request: {"dortania_url", "ocbinary_url", "ock_dir", "macos"}
Output:  {"ok": bool|null, "error": str|null, "tree": {rel: sha256 | {manifest}}, "history": str|null}

Only the orchestration is exercised. Messages are discarded, and the interactive
pause (request_input) and the terminal clear (head) are disabled, as they are
terminal-only effects.
"""

import hashlib
import json
import os
import sys
import tempfile

repo = sys.argv[1]
sys.path.insert(0, repo)

from Scripts import gathering_files, github, integrity_checker, kext_maestro, resource_fetcher, utils  # noqa: E402

real_stdout = sys.stdout
sys.stdout = open(os.devnull, "w")
utils.Utils.head = lambda self, *a, **k: None
utils.Utils.request_input = lambda self, *a, **k: ""

req = json.load(sys.stdin)

g = gathering_files.gatheringFiles.__new__(gathering_files.gatheringFiles)
g.utils = utils.Utils()
g.github = github.Github()
g.kext = None
g.fetcher = resource_fetcher.ResourceFetcher()
g.integrity_checker = integrity_checker.IntegrityChecker()
g.dortania_builds_url = req["dortania_url"]
g.ocbinarydata_url = req["ocbinary_url"]
g.temporary_dir = tempfile.mkdtemp(prefix="ocs_py_")
g.ock_files_dir = req["ock_dir"]
g.download_history_file = os.path.join(req["ock_dir"], "history.json")

kexts = []
if req.get("kext"):
    from Scripts.datasets import kext_data  # noqa: E402

    spec = req["kext"]
    kext = kext_data.KextInfo(
        name="Lilu",
        description="test",
        category="test",
        download_info={"id": spec["id"], "url": spec["url"], "sha256": spec["sha256"]},
    )
    kext.checked = True
    kexts = [kext]
    g.kext = kext_maestro.KextMaestro()

error = None
ok = None
try:
    ok = g.gather_bootloader_kexts(kexts, req["macos"])
except Exception as exc:
    error = type(exc).__name__


def snapshot(root):
    tree = {}
    if not os.path.isdir(root):
        return tree
    for dirpath, _, files in os.walk(root):
        for name in files:
            full = os.path.join(dirpath, name)
            rel = os.path.relpath(full, root).replace("\\", "/")
            if name == "manifest.json":
                with open(full, "rb") as f:
                    tree[rel] = {"manifest": json.loads(f.read().decode("utf-8"))}
            else:
                with open(full, "rb") as f:
                    tree[rel] = hashlib.sha256(f.read()).hexdigest()
    return tree


history_path = g.download_history_file
history = None
if os.path.exists(history_path):
    with open(history_path, "r", encoding="utf-8") as f:
        history = f.read()

sys.stdout = real_stdout
json.dump({"ok": ok, "error": error, "tree": snapshot(req["ock_dir"]), "history": history}, sys.stdout)
