"""Reference oracle for go/internal/fetch, using the ORIGINAL resource_fetcher.py.

Usage: python3 -I pyref_fetch.py <repo_root>
Stdin: JSON list of {"op": "fetch", "url", "type"} or
       {"op": "download", "url", "dest", "sha256"} (sha256 may be null).
Stdout (only the results; the original's messages go to /dev/null):
       fetch -> {"ok": <json.dumps(result)> or null}
       download -> {"ok": bool, "file_sha256": str or null}
"""

import hashlib
import json
import os
import sys

sys.path.insert(0, sys.argv[1])

from Scripts import resource_fetcher  # noqa: E402

real_stdout = sys.stdout
sys.stdout = open(os.devnull, "w")
fetcher = resource_fetcher.ResourceFetcher()

results = []
for req in json.load(sys.stdin):
    if req["op"] == "fetch":
        value = fetcher.fetch_and_parse_content(req["url"], req["type"])
        results.append({"ok": None if value is None else json.dumps(value, indent=4)})
    else:
        ok = fetcher.download_and_save_file(req["url"], req["dest"], req["sha256"])
        digest = None
        if os.path.exists(req["dest"]):
            with open(req["dest"], "rb") as f:
                digest = hashlib.sha256(f.read()).hexdigest()
        results.append({"ok": {"bool": ok, "file_sha256": digest}})
sys.stdout = real_stdout
json.dump(results, sys.stdout)
