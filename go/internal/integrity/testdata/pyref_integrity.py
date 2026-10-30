"""Reference oracle for go/internal/integrity, using the ORIGINAL integrity_checker.py.

Usage: python3 -I pyref_integrity.py <repo_root>
Stdin: JSON list of {"op": "manifest", "folder", "manifest"} or
       {"op": "verify", "folder", "manifest"} (manifest may be null).
Stdout: per request, {"manifest": {...}} | {"valid": bool, "issues": {...}} | {"message": str}
"""

import json
import sys

sys.path.insert(0, sys.argv[1])

from Scripts import integrity_checker  # noqa: E402

checker = integrity_checker.IntegrityChecker()
results = []
for req in json.load(sys.stdin):
    if req["op"] == "manifest":
        data = checker.generate_folder_manifest(req["folder"], req["manifest"])
        results.append({"manifest": data})
        continue
    valid, issues = checker.verify_folder_integrity(req["folder"], req["manifest"])
    if valid is None:
        results.append({"message": issues})
    else:
        results.append({"valid": valid, "issues": {k: sorted(v) for k, v in issues.items()}})
json.dump(results, sys.stdout)
