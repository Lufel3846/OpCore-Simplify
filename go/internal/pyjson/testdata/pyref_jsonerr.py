"""Oracle for the error messages of go/internal/pyjson (ORIGINAL Python json module).

Stdin: JSON list of {"hex": bytes as hex, "mode": "file" | "loads"}.
"file" is json.load(open(path, "r")); "loads" is json.loads(bytes).
Stdout: per case {"ok": json.dumps(value) } or {"type": exception name, "message": str(e)}.
"""

import json
import os
import sys
import tempfile

out = []
with tempfile.TemporaryDirectory() as tmp:
    for i, case in enumerate(json.load(sys.stdin)):
        data = bytes.fromhex(case["hex"])
        try:
            if case["mode"] == "file":
                path = os.path.join(tmp, "case%d.json" % i)
                with open(path, "wb") as f:
                    f.write(data)
                with open(path, "r") as f:
                    value = json.load(f)
            else:
                value = json.loads(data)
            out.append({"ok": json.dumps(value, indent=4)})
        except Exception as e:  # noqa: BLE001
            out.append({"type": type(e).__name__, "message": str(e)})
sys.stdout.write(json.dumps(out))
