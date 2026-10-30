"""Reference oracle for go/internal/wifi, running the ORIGINAL wifi_profile_extractor.py.

Usage: python3 -I pyref_wifi.py <repo_root>   (case list on stdin)
Each case: {"op", "os", "args", "commands": [{"args", "out", "rc"}], "inputs": [...]}
  op "auth": args [text]                      -> get_authentication_type
  op "validate": args [auth|null, password]    -> validate_wifi_password
  op "count": args [total]                     -> ask_network_count
  op "macpass" | "winpass" | "linuxpass": args [ssid] -> get_wifi_password_*
  op "profiles"                                -> get_profiles
Output per case: {"ok": json.dumps(value) | null, "error": name | null, "prompts": [...]}

Commands are answered from the case table (a missing command returns rc 1), and the
prompts are answered from "inputs" in order; when they run out, the answer is "no".
The original's terminal output is discarded.
"""

import json
import os
import platform
import sys

repo = sys.argv[1]
sys.path.insert(0, repo)

from Scripts import utils, wifi_profile_extractor as w  # noqa: E402

real_stdout = sys.stdout
sys.stdout = open(os.devnull, "w")
utils.Utils.head = lambda self, *a, **k: None

results = []
for case in json.load(sys.stdin):
    table = {" ".join(c["args"]): (c["out"], c["rc"]) for c in case["commands"] or []}
    inputs = list(case["inputs"] or [])
    prompts = []

    def fake_input(self, prompt="Press Enter to continue..."):
        prompts.append(prompt)
        return inputs.pop(0) if inputs else "no"

    utils.Utils.request_input = fake_input
    w.os_name = case["os"]
    platform.system = lambda: case["os"]

    ex = w.WifiProfileExtractor.__new__(w.WifiProfileExtractor)
    ex.utils = utils.Utils()

    def run(command):
        out, rc = table.get(" ".join(command["args"]), ("", 1))
        return (out, "", rc)

    ex.run = run
    op = case["op"]
    args = case["args"]
    try:
        if op == "auth":
            value = ex.get_authentication_type(args[0])
        elif op == "validate":
            value = ex.validate_wifi_password(args[0], args[1])
        elif op == "count":
            value = ex.ask_network_count(args[0])
        elif op == "macpass":
            value = ex.get_wifi_password_macos(args[0])
        elif op == "winpass":
            value = ex.get_wifi_password_windows(args[0])
        elif op == "linuxpass":
            value = ex.get_wifi_password_linux(args[0])
        else:
            value = ex.get_profiles()
        results.append({"ok": json.dumps([list(p) for p in value] if op == "profiles" else value), "error": None, "prompts": prompts})
    except Exception as exc:
        results.append({"ok": None, "error": type(exc).__name__, "prompts": prompts})

sys.stdout = real_stdout
json.dump(results, sys.stdout)
