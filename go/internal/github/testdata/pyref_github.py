"""Reference oracle for go/internal/github, calling the ORIGINAL github.py parsers.

Usage: python3 -I pyref_github.py <repo_root>
Stdin: JSON list of {"op": "payload"|"tag"|"body"|"assets"|"name", "text": str}.
Stdout: per request {"ok": json.dumps(result, indent=4)} (None -> null) or {"error": ...}.
"""

import json
import sys

sys.path.insert(0, sys.argv[1])

from Scripts import github  # noqa: E402

g = github.Github.__new__(github.Github)  # parsers need no fetcher
out = []
for req in json.load(sys.stdin):
    try:
        if req["op"] == "payload":
            value = g.extract_payload(req["text"])
        elif req["op"] == "tag":
            value = g._extract_tag_name(req["text"])
        elif req["op"] == "body":
            value = g._extract_body_content(req["text"])
        elif req["op"] == "assets":
            value = g._extract_assets(req["text"])
        else:
            value = g.extract_asset_name(req["text"])
        out.append({"ok": None if value is None else json.dumps(value, indent=4)})
    except Exception as exc:
        out.append({"error": type(exc).__name__})
json.dump(out, sys.stdout)
