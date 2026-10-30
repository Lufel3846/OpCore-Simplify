"""Reference oracle for the Go package go/internal/smbios.

Runs the ORIGINAL SMBIOS.select_smbios_model (Scripts/smbios.py) and the
mac_devices table (Scripts/datasets/mac_model_data.py) without modifying them.

Usage:  python3 -I pyref_smbios.py <repo_root>
Stdin:  JSON list of requests:
        {"op": "devices"}
        {"op": "select", "macos": "25.0.0",
         "arg": {"motherboard": {"name", "platform"},
                 "cpu": {"codename", "processor_name", "core_count"},
                 "gpus": [{"device_type"}, ...]}}
Stdout: JSON list of {"ok": ...} or {"error": "<ExceptionName>"}, in request order.
"""

import json
import sys

sys.path.insert(0, sys.argv[1])

from Scripts import smbios, utils  # noqa: E402
from Scripts.datasets.mac_model_data import mac_devices  # noqa: E402


def build_report(arg):
    gpus = {"GPU {}".format(i): {"Device Type": g["device_type"]} for i, g in enumerate(arg["gpus"])}
    return {
        "Motherboard": {"Name": arg["motherboard"]["name"], "Platform": arg["motherboard"]["platform"]},
        "CPU": {
            "Codename": arg["cpu"]["codename"],
            "Processor Name": arg["cpu"]["processor_name"],
            "Core Count": arg["cpu"]["core_count"],
        },
        "GPU": gpus,
    }


def run(req):
    try:
        if req["op"] == "devices":
            return {
                "ok": [
                    [d.name, d.cpu, d.cpu_generation, d.discrete_gpu, d.initial_support, d.last_supported_version]
                    for d in mac_devices
                ]
            }
        if req["op"] == "select":
            model = smbios.SMBIOS.__new__(smbios.SMBIOS)  # skip __init__: no I/O needed here
            model.utils = utils.Utils.__new__(utils.Utils)
            return {"ok": model.select_smbios_model(build_report(req["arg"]), req["macos"])}
        raise ValueError("unknown op " + req["op"])
    except Exception as exc:
        return {"error": type(exc).__name__}


if __name__ == "__main__":
    json.dump([run(r) for r in json.load(sys.stdin)], sys.stdout)
