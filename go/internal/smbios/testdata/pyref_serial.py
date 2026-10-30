"""Reference oracle for go/internal/smbios serial generation, using the ORIGINAL smbios.py.

Usage: python3 -I pyref_serial.py <repo_root>   (scenario list on stdin)
Each scenario is run in a fresh folder. The executable is never run: the oracle
replaces SMBIOS.run with the scenario's stdout and return code, and the gather
step with a function that creates the scenario's binaries.
"""

import json
import os
import sys
import tempfile
import uuid

repo = sys.argv[1]
sys.path.insert(0, repo)

from Scripts import smbios, utils  # noqa: E402


def read_text(path):
    if not os.path.exists(path):
        return None
    with open(path, "r", encoding="utf-8") as f:
        return f.read()


results = []
for sc in json.load(sys.stdin):
    tmp = tempfile.mkdtemp(prefix="ocs_serial_")
    script_dir = os.path.join(tmp, "Scripts")
    os.makedirs(script_dir)
    history_file = os.path.join(tmp, "history.json")
    for name in sc["binaries"] or []:
        open(os.path.join(script_dir, name), "w").close()
    if sc["history"] is not None:
        with open(history_file, "w", encoding="utf-8") as f:
            f.write(sc["history"])

    class Gather:
        download_history_file = history_file

        def gather_bootloader_kexts(self, kexts, macos_version):
            for name in sc["gather_creates"] or []:
                open(os.path.join(script_dir, name), "w").close()
            return True

    sm = smbios.SMBIOS.__new__(smbios.SMBIOS)
    sm.g = Gather()
    sm.utils = utils.Utils()
    sm.script_dir = script_dir
    sm.run = lambda command: (sc["stdout"], "", sc["rc"])
    sm.generate_random_mac = lambda: sc["mac"]
    smbios.os_name = sc["os"]
    uuid.uuid4 = lambda: uuid.UUID(sc["uuid"])

    try:
        value = sm.generate_smbios(sc["model"])
        ok = json.dumps(value, indent=4)
        error = None
    except Exception as exc:
        ok = None
        error = type(exc).__name__
    results.append({"ok": ok, "error": error, "history": read_text(history_file)})

json.dump(results, sys.stdout)
