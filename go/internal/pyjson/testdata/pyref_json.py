"""Reference oracle for go/internal/pyjson, using the ORIGINAL Python json module.

Stdin: JSON list of document texts (JSON-encoded strings).
Stdout: per document, {"ok": json.dumps(json.loads(doc), indent=4)} or {"error": ...}.
"""

import json
import sys

out = []
for doc in json.load(sys.stdin):
    try:
        out.append({"ok": json.dumps(json.loads(doc), indent=4)})
    except Exception as exc:
        out.append({"error": type(exc).__name__})
json.dump(out, sys.stdout)
