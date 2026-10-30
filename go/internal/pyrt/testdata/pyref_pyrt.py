"""Reference oracle for go/internal/pyrt: evaluates each case with the real Python operators.

Stdin: JSON list of {"op": "in"|"eq"|"truthy"|"len"|"int", "a": ..., "b": ...}.
Each result is {"ok": <python value>} or {"error": <exception name>}; "in" and "int" map
Python's exceptions to the same names the Go helpers return.
"""

import json
import sys

out = []
for req in json.load(sys.stdin):
    op = req["op"]
    try:
        if op == "in":
            value = req["a"] in req["b"]
        elif op == "eq":
            value = req["a"] == req["b"]
        elif op == "truthy":
            value = bool(req["a"])
        elif op == "len":
            value = len(req["a"])
        else:
            value = int(req["a"])
        out.append({"ok": value})
    except Exception as exc:
        out.append({"error": type(exc).__name__})
json.dump(out, sys.stdout)
