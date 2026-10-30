"""Reference oracle for go/internal/efi, running the ORIGINAL OCPE.build_opencore_efi
from OpCore-Simplify.py.

Usage: python3 -I pyref_efi.py <repo_root> <testdata_dir> <ock_dir> <result_dir>  (request on stdin)
Request: {"report", "disabled", "smbios_model", "macos", "needs_oclp", "checked": [names]}
Output:  {"ok": {relative path: sha256}} or {"error": "<ExceptionName>"}

Fixed by the oracle, the same way as the config parity test: the ACPI stage reports
ensure_dsdt() == False, the layout prompt is answered with the default, random.choice
returns the first candidate, the kernel patches come from patches.plist, the serial is
fixed, and time.sleep is skipped.
"""

import hashlib
import importlib.util
import json
import os
import plistlib
import random
import sys
import time

repo, testdata, ock, result = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
sys.path.insert(0, repo)

from Scripts import config_prodigy, gathering_files, kext_maestro, smbios, utils  # noqa: E402
from Scripts.datasets import kext_data  # noqa: E402

SERIAL = {
    "MLB": "C02TESTMLB000000",
    "ROM": "A1B2C3D4E5F6",
    "SystemProductName": "MacPro7,1",
    "SystemSerialNumber": "C02TESTSERIAL",
    "SystemUUID": "00000000-0000-0000-0000-000000000000",
}


class StubGathering:
    def __init__(self):
        self.amd_vanilla_patches_url = "amd-url"
        self.aquantia_macos_patches_url = "aquantia-url"
        self.hyper_threading_patches_url = "ht-url"

    def get_kernel_patches(self, patches_name, patches_url):
        with open(os.path.join(testdata, "patches.plist"), "rb") as f:
            return plistlib.load(f)["Kernel"]["Patch"]


class StubSMBIOS:
    def generate_smbios(self, model):
        return dict(SERIAL)


class StubACPI:
    dsdt_patches = []
    patches = []

    def ensure_dsdt(self, allow_any=False):
        return False

    def apply_acpi_patches(self, acpi_patches):
        return acpi_patches


gathering_files.gatheringFiles = StubGathering
smbios.SMBIOS = StubSMBIOS
utils.Utils.request_input = lambda self, prompt="Press Enter to continue...": ""
utils.Utils.head = lambda self, *a, **k: None
random.choice = lambda seq: seq[0]
time.sleep = lambda seconds: None

spec = importlib.util.spec_from_file_location("ocpe", os.path.join(repo, "OpCore-Simplify.py"))
ocpe = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ocpe)

req = json.load(sys.stdin)

real_stdout = sys.stdout
sys.stdout = open(os.devnull, "w")

for k in kext_data.kexts:
    k.checked = k.required
for name in req["checked"]:
    kext_data.kexts[kext_data.kext_index_by_name[name]].checked = True

obj = ocpe.OCPE.__new__(ocpe.OCPE)
obj.u = utils.Utils("OpCore Simplify")
obj.result_dir = result
obj.k = kext_maestro.KextMaestro()
obj.k.ock_files_dir = ock
obj.ac = StubACPI()
obj.co = config_prodigy.ConfigProdigy()

error = None
try:
    obj.build_opencore_efi(req["report"], req["disabled"], req["smbios_model"], req["macos"], req["needs_oclp"])
except Exception as exc:
    error = type(exc).__name__

tree = {}
if os.path.isdir(result):
    for dirpath, _, files in os.walk(result):
        for name in files:
            full = os.path.join(dirpath, name)
            with open(full, "rb") as f:
                tree[os.path.relpath(full, result).replace("\\", "/")] = hashlib.sha256(f.read()).hexdigest()

sys.stdout = real_stdout
if error:
    json.dump({"error": error}, sys.stdout)
else:
    json.dump({"ok": dict(sorted(tree.items()))}, sys.stdout)
