"""Reference oracle for go/internal/config, running the ORIGINAL config_prodigy.py.

Usage: python3 -I pyref_config.py <repo_root> <testdata_dir>  (one JSON request list on stdin)
Each request: {"report", "disabled", "smbios_model", "macos", "needs_oclp", "checked": [names]}
Output per request: {"ok": "<config.plist as XML text>"} or {"error": "<ExceptionName>"}

Fixed by the oracle (both implementations use the same values):
  - the layout prompt is answered with the empty string (default layout)
  - random.choice always returns the first candidate
  - get_kernel_patches reads patches.plist from the testdata folder (fresh copy per call)
  - generate_smbios returns the fixed values in SERIAL
"""

import json
import os
import plistlib
import random
import sys

repo, testdata = sys.argv[1], sys.argv[2]
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


gathering_files.gatheringFiles = StubGathering
smbios.SMBIOS = StubSMBIOS
utils.Utils.request_input = lambda self, prompt="Press Enter to continue...": ""
utils.Utils.head = lambda self, *a, **k: None
random.choice = lambda seq: seq[0]

real_stdout = sys.stdout
sys.stdout = open(os.devnull, "w")
cp = config_prodigy.ConfigProdigy()
km_kexts = kext_data.kexts

results = []
for req in json.load(sys.stdin):
    for k in km_kexts:
        k.checked = k.required
    for name in req["checked"]:
        km_kexts[kext_data.kext_index_by_name[name]].checked = True
    with open(os.path.join(testdata, "Sample.plist"), "rb") as f:
        config = plistlib.load(f)
    try:
        out = cp.genarate(req["report"], req["disabled"], req["smbios_model"], req["macos"],
                          req["needs_oclp"], km_kexts, config)
        results.append({"ok": plistlib.dumps(out, fmt=plistlib.FMT_XML).decode("utf-8")})
    except Exception as exc:
        results.append({"error": type(exc).__name__})

sys.stdout = real_stdout
json.dump(results, sys.stdout)
