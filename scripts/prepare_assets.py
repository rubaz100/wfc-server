#!/usr/bin/env python3
"""Copy reviewed public runtime artifacts and record their provenance."""

import hashlib
import json
import re
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
PATCHER = ROOT.parent / "wfc-patcher-wii"
DESTINATION = ROOT / "internal" / "assets" / "files"
REGIONS = ("P", "E", "J", "K")
ARTIFACTS = [
    (ROOT / "payload" / "stage1.bin", "stage1.bin"),
    (ROOT / "payload" / "stage1v1.bin", "stage1v1.bin"),
    (ROOT / "game_list.tsv", "game_list.tsv"),
]
ARTIFACTS += [
    (ROOT / "payload" / "binary" / f"payload.RMC{region}{suffix}.bin",
     f"binary/payload.RMC{region}{suffix}.bin")
    for region in REGIONS for suffix in ("D00", "N0001")
]
ARTIFACTS += [
    (ROOT / "payload" / "sbcm" / f"payload.RMC{region}.bin",
     f"sbcm/payload.RMC{region}.bin")
    for region in REGIONS
]
ARTIFACTS += [
    (PATCHER / "patch" / "build" / f"RMC{region}{suffix}.bin",
     f"gecko/RMC{region}{suffix}.bin")
    for region in REGIONS for suffix in ("D00", "N0001")
]


def revision(path):
    return subprocess.check_output(
        ["git", "-C", str(path), "rev-parse", "HEAD"], text=True
    ).strip()


def development_public_key():
    text = (PATCHER / "include" / "wwfcPayloadPublicKey.hpp").read_text()
    development = text.split("#else", 1)[1].split("#endif", 1)[0]
    match = re.search(r"PayloadPublicKey\[\] = \{(.*?)\};", development, re.S)
    if not match:
        raise RuntimeError("development payload public key is missing")
    key = bytes(int(value, 16) for value in re.findall(r"0x([0-9a-fA-F]{2})", match[1]))
    if len(key) != 516:
        raise RuntimeError(f"development public key has {len(key)} bytes, want 516")
    return key


def main():
    if not PATCHER.is_dir():
        raise SystemExit(f"missing public payload source: {PATCHER}")
    key = development_public_key()
    artifacts = []
    for source, relative in ARTIFACTS:
        data = source.read_bytes()
        if relative in ("stage1.bin", "stage1v1.bin") or relative.startswith("sbcm/"):
            if data.count(key) != 1:
                raise RuntimeError(f"{relative}: expected exactly one development public key")
        destination = DESTINATION / relative
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.write_bytes(data)
        artifacts.append({
            "path": relative,
            "source": str(source.relative_to(ROOT.parent)),
            "size": len(data),
            "sha256": hashlib.sha256(data).hexdigest(),
        })
    manifest = {
        "format": 1,
        "payload_version": "0.3.1",
        "wfc_server_revision": revision(ROOT),
        "patcher_revision": revision(PATCHER),
        "development_public_key_sha256": hashlib.sha256(key).hexdigest(),
        "development_public_key": key.hex(),
        "artifacts": artifacts,
    }
    (DESTINATION / "manifest.json").write_text(
        json.dumps(manifest, indent=2, sort_keys=True) + "\n"
    )
    print(f"Prepared {len(artifacts)} public artifacts; no private keys copied.")


if __name__ == "__main__":
    main()
