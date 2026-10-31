"""Reference oracle for go/internal/validator, running the ORIGINAL ReportValidator.

Usage: python3 -I pyref_validator.py <repo_root>   (list of report paths on stdin)
Output per path: {"valid", "errors", "warnings", "cleaned"}; dicts in "cleaned"
become {"__dict__": [[key, value], ...]} so key order is compared too.
"""

import json
import os
import sys

repo = sys.argv[1]
sys.path.insert(0, repo)

from Scripts import report_validator  # noqa: E402

real_stdout = sys.stdout
sys.stdout = open(os.devnull, "w")


def conv(v):
    if isinstance(v, bytes):
        return {"hex": v.hex()}
    if isinstance(v, dict):
        return {"__dict__": [[k, conv(x)] for k, x in v.items()]}
    if isinstance(v, (list, tuple)):
        return [conv(x) for x in v]
    if isinstance(v, float) and v != v:
        return {"float": "nan"}
    if isinstance(v, float) and v in (float("inf"), float("-inf")):
        return {"float": repr(v)}
    return v


results = []
for path in json.load(sys.stdin):
    v = report_validator.ReportValidator()
    valid, errors, warnings, cleaned = v.validate_report(path)
    results.append({"valid": valid, "errors": errors, "warnings": warnings, "cleaned": conv(cleaned)})
real_stdout.write(json.dumps(results))
