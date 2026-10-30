"""Reference oracle for the Go package go/internal/plist.

Runs the ORIGINAL plistlib (the library used by Scripts/utils.py) without
modification. Values travel as tagged JSON because plain JSON cannot tell an
int from a float, or str from bytes.

Usage:  python3 -I pyref_plist.py
Stdin:  JSON list of requests:
        {"op": "dumps", "v": <tagged>}   -> {"ok": "<xml text>"} | {"error": "..."}
        {"op": "loads", "xml": "<text>"} -> {"ok": <tagged>}     | {"error": "..."}
Tagged values: {"t": "dict", "v": [[key, tagged], ...]}, {"t": "list", "v": [...]},
{"t": "str", "v": s}, {"t": "int", "v": "<decimal>"}, {"t": "float", "v": "<repr>"},
{"t": "bool", "v": b}, {"t": "data", "v": "<base64>"},
{"t": "date", "v": "YYYY-MM-DDTHH:MM:SS"}, {"t": "none"}.
"""

import base64
import datetime
import json
import plistlib
import sys


def dec(t):
    kind = t["t"]
    if kind == "none":
        return None
    v = t["v"]
    if kind == "dict":
        d = {}
        for key, val in v:
            d[key] = dec(val)
        return d
    if kind == "list":
        return [dec(x) for x in v]
    if kind == "str":
        return v
    if kind == "int":
        return int(v)
    if kind == "float":
        return float(v)
    if kind == "bool":
        return v
    if kind == "data":
        return base64.b64decode(v)
    if kind == "date":
        return datetime.datetime.strptime(v, "%Y-%m-%dT%H:%M:%S")
    raise ValueError("unknown tag " + kind)


def enc(o):
    if o is None:
        return {"t": "none"}
    if isinstance(o, bool):
        return {"t": "bool", "v": o}
    if isinstance(o, int):
        return {"t": "int", "v": str(o)}
    if isinstance(o, float):
        return {"t": "float", "v": repr(o)}
    if isinstance(o, str):
        return {"t": "str", "v": o}
    if isinstance(o, (bytes, bytearray)):
        return {"t": "data", "v": base64.b64encode(bytes(o)).decode()}
    if isinstance(o, datetime.datetime):
        return {"t": "date", "v": o.strftime("%Y-%m-%dT%H:%M:%S")}
    if isinstance(o, dict):
        return {"t": "dict", "v": [[k, enc(val)] for k, val in o.items()]}
    if isinstance(o, list):
        return {"t": "list", "v": [enc(x) for x in o]}
    raise TypeError("cannot encode " + type(o).__name__)


def run(req):
    try:
        if req["op"] == "dumps":
            return {"ok": plistlib.dumps(dec(req["v"]), fmt=plistlib.FMT_XML).decode("utf-8")}
        if req["op"] == "loads":
            return {"ok": enc(plistlib.loads(req["xml"].encode("utf-8"), fmt=plistlib.FMT_XML))}
        raise ValueError("unknown op " + req["op"])
    except Exception as exc:
        return {"error": type(exc).__name__}


if __name__ == "__main__":
    json.dump([run(r) for r in json.load(sys.stdin)], sys.stdout)
