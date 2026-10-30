"""Reference oracle for the Go package go/internal/macos.

Runs the ORIGINAL OpCore Simplify code (Scripts/datasets/os_data.py and
Scripts/utils.py) without modifying it.

Usage:  python3 -I pyref_macos.py <repo_root>
Stdin:  JSON list of requests, e.g. [{"op": "parse", "arg": "25.0.0"}]
Stdout: JSON list of results, one per request, in the same order:
        {"ok": <value>}                 on success (None is returned as null)
        {"error": "<ExceptionName>"}    when the original raises
"""

import json
import sys

sys.path.insert(0, sys.argv[1])

from Scripts import utils  # noqa: E402
from Scripts.datasets import os_data  # noqa: E402


def run(req):
    op = req["op"]
    arg = req.get("arg")
    try:
        if op == "parse":
            helper = utils.Utils.__new__(utils.Utils)  # no __init__ side effects
            return {"ok": list(helper.parse_darwin_version(arg))}
        if op == "latest":
            return {"ok": os_data.get_latest_darwin_version(arg)}
        if op == "lowest":
            return {"ok": os_data.get_lowest_darwin_version()}
        if op == "name":
            return {"ok": os_data.get_macos_name_by_darwin(arg)}
        if op == "catalog":
            return {
                "ok": [
                    [d.name, d.macos_version, d.darwin_version, d.release_status]
                    for d in os_data.macos_versions
                ]
            }
        raise ValueError("unknown op " + op)
    except Exception as exc:  # the error type is part of the contract
        return {"error": type(exc).__name__}


if __name__ == "__main__":
    requests = json.load(sys.stdin)
    json.dump([run(r) for r in requests], sys.stdout)
