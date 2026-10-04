#!/usr/bin/env python3
"""Install NVIDIA redist components (CUDA, cuDNN) for Jetson.

    nvidia-redist.py MANIFEST_URL DEST COMPONENT...

MANIFEST_URL is a redistrib_<version>.json, such as
https://developer.download.nvidia.com/compute/cuda/redist/redistrib_12.2.0.json.
Each component's linux-aarch64 (Tegra) archive is downloaded, checked against
the manifest's SHA-256 and unpacked into DEST, merging bin/, include/, lib/
and the rest. A component with a CUDA variant (cuDNN's "cuda12") uses it.
"""
import hashlib
import json
import os
import shutil
import sys
import tarfile
import tempfile
import urllib.request

PLATFORM = "linux-aarch64"


def main(manifest_url, dest, components):
    base = manifest_url.rsplit("/", 1)[0] + "/"
    with urllib.request.urlopen(manifest_url) as r:
        manifest = json.load(r)
    os.makedirs(dest, exist_ok=True)
    for name in components:
        entry = manifest[name][PLATFORM]
        if "relative_path" not in entry:
            entry = entry["cuda12"]
        url = base + entry["relative_path"]
        with tempfile.TemporaryDirectory() as tmp:
            archive = os.path.join(tmp, "archive.tar.xz")
            h = hashlib.sha256()
            with urllib.request.urlopen(url) as r, open(archive, "wb") as f:
                while chunk := r.read(1 << 20):
                    h.update(chunk)
                    f.write(chunk)
            if h.hexdigest() != entry["sha256"]:
                sys.exit(f"{name}: SHA-256 {h.hexdigest()}, manifest says {entry['sha256']}")
            with tarfile.open(archive) as t:
                t.extractall(tmp, filter="tar")
            top = os.path.join(tmp, entry["relative_path"].rsplit("/", 1)[1].removesuffix(".tar.xz"))
            shutil.copytree(top, dest, symlinks=True, dirs_exist_ok=True)
        print(f"{name} {manifest[name]['version']}: verified and installed in {dest}", flush=True)


if __name__ == "__main__":
    if len(sys.argv) < 4:
        sys.exit(__doc__)
    main(sys.argv[1], sys.argv[2], sys.argv[3:])
