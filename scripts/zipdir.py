#!/usr/bin/env python3
"""Zip a directory flat-rooted (entries relative to it), deterministically.

usage: zipdir.py SRC_DIR OUT.zip REQUIRED_FILE...

Fails if a required file is missing, so a broken publish never becomes a release.
"""
import os
import sys
import zipfile


def main() -> int:
    if len(sys.argv) < 3:
        print(__doc__, file=sys.stderr)
        return 2
    src, out, required = sys.argv[1], sys.argv[2], sys.argv[3:]
    files = []
    for root, _dirs, names in os.walk(src):
        for name in names:
            full = os.path.join(root, name)
            files.append((os.path.relpath(full, src).replace(os.sep, "/"), full))
    files.sort()
    rels = {rel for rel, _ in files}
    missing = [r for r in required if r not in rels]
    if missing:
        print("zipdir: missing from " + src + ": " + ", ".join(missing), file=sys.stderr)
        return 1
    tmp = out + ".tmp"
    with zipfile.ZipFile(tmp, "w", zipfile.ZIP_DEFLATED) as z:
        for rel, full in files:
            info = zipfile.ZipInfo(rel, date_time=(1980, 1, 1, 0, 0, 0))  # reproducible
            info.compress_type = zipfile.ZIP_DEFLATED
            info.external_attr = 0o644 << 16
            with open(full, "rb") as f:
                z.writestr(info, f.read())
    os.replace(tmp, out)
    print(f"zipdir: {len(files)} files -> {out} ({os.path.getsize(out)} bytes)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
