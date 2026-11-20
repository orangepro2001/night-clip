#!/usr/bin/env python3
"""Zip a macOS .app bundle with Unix modes, for hosts without `zip -y` (Windows).

usage: zipapp.py BASE_DIR APP_NAME OUT.zip

Entries are stored relative to BASE_DIR, starting with APP_NAME/. Symlinks are kept as
symlinks. Files that are Mach-O binaries (or already executable on a POSIX host) get mode
0755, everything else 0644, so `ditto -x -k` on the Mac restores a launchable app even
though Windows has no execute bit.
"""
import os
import stat
import sys
import zipfile

MACHO = {
    b"\xcf\xfa\xed\xfe", b"\xce\xfa\xed\xfe",  # 64/32-bit, little endian
    b"\xfe\xed\xfa\xcf", b"\xfe\xed\xfa\xce",  # big endian
    b"\xca\xfe\xba\xbe", b"\xbe\xba\xfe\xca",  # universal
}


def mode_for(path: str) -> int:
    st = os.stat(path)
    if os.name != "nt" and st.st_mode & 0o111:
        return 0o755
    with open(path, "rb") as f:
        return 0o755 if f.read(4) in MACHO else 0o644


def main() -> int:
    if len(sys.argv) != 4:
        print(__doc__, file=sys.stderr)
        return 2
    base, app, out = sys.argv[1], sys.argv[2], sys.argv[3]
    root = os.path.join(base, app)
    if not os.path.isdir(root):
        print(f"zipapp: {root} is not a directory", file=sys.stderr)
        return 1
    tmp = out + ".tmp"
    count = 0
    with zipfile.ZipFile(tmp, "w", zipfile.ZIP_DEFLATED) as z:
        for dirpath, dirnames, filenames in os.walk(root):
            dirnames.sort()
            rel_dir = os.path.relpath(dirpath, base).replace(os.sep, "/")
            info = zipfile.ZipInfo(rel_dir + "/")
            info.external_attr = (stat.S_IFDIR | 0o755) << 16
            z.writestr(info, b"")
            for name in sorted(filenames):
                full = os.path.join(dirpath, name)
                rel = rel_dir + "/" + name
                info = zipfile.ZipInfo(rel)
                info.create_system = 3  # Unix: the attributes below are honoured
                if os.path.islink(full):
                    info.external_attr = (stat.S_IFLNK | 0o777) << 16
                    z.writestr(info, os.readlink(full))
                else:
                    info.compress_type = zipfile.ZIP_DEFLATED
                    info.external_attr = (stat.S_IFREG | mode_for(full)) << 16
                    with open(full, "rb") as f:
                        z.writestr(info, f.read())
                count += 1
    os.replace(tmp, out)
    print(f"zipapp: {count} files -> {out} ({os.path.getsize(out)} bytes)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
