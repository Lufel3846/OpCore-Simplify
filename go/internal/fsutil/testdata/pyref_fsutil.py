"""Reference oracle for go/internal/fsutil, using the ORIGINAL Scripts/utils.py.

Usage: python3 -I pyref_fsutil.py <repo_root>
Stdin: {"find": [{"root", "ext", "name", "type"}], "write": {"out_json", "out_plist", "json_text", "plist_text"}}
Stdout: {"find": [[[path, type], ...], ...], "json": "<bytes as text>", "plist": "<bytes as base64>"}
"""

import base64
import json
import os
import sys

sys.path.insert(0, sys.argv[1])

from Scripts import utils  # noqa: E402

u = utils.Utils()
req = json.load(sys.stdin)
out = {"find": [], "json": None, "plist": None}

for f in req["find"]:
    # The original's "no filter" is None; an empty string matches nothing for type.
    matches = u.find_matching_paths(
        f["root"],
        extension_filter=f["ext"] or None,
        name_filter=f["name"] or None,
        type_filter=f["type"] or None,
    )
    out["find"].append([[path, kind] for path, kind in matches])

w = req["write"]
u.write_file(w["out_json"], json.loads(w["json_text"]))
with open(w["out_json"], "r", encoding="utf-8") as fh:
    out["json"] = fh.read()

import plistlib  # noqa: E402
u.write_file(w["out_plist"], plistlib.loads(base64.b64decode(w["plist_b64"])))
with open(w["out_plist"], "rb") as fh:
    out["plist"] = base64.b64encode(fh.read()).decode()

json.dump(out, sys.stdout)
