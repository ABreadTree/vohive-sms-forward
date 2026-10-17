#!/usr/bin/env python3
"""Package an already-built ARM64/musl binary for the verified iStoreOS target.

Uses OpenWrt's gzip/tar IPK layout (not Debian's ar layout). No remote access,
credentials, dependency installation, or service start is performed here.
"""
import argparse
import gzip
import hashlib
import io
import json
from pathlib import Path
import re
import struct
import tarfile


def validate_binary(data):
    if len(data) < 64 or data[:7] != b"\x7fELF\x02\x01\x01":
        raise ValueError("expected a little-endian ELF64 executable")
    if struct.unpack_from("<H", data, 18)[0] != 183:
        raise ValueError("expected AArch64 machine type")
    if struct.unpack_from("<H", data, 16)[0] not in (2, 3):
        raise ValueError("expected an executable or PIE, not a relocatable object")
    offset = struct.unpack_from("<Q", data, 32)[0]
    size, count = struct.unpack_from("<HH", data, 54)
    if size < 56 or not count or offset + size * count > len(data):
        raise ValueError("invalid ELF program headers")
    interpreter = False
    loadable = False
    for index in range(count):
        ph = offset + index * size
        kind = struct.unpack_from("<I", data, ph)[0]
        if kind == 0:
            continue
        start = struct.unpack_from("<Q", data, ph + 8)[0]
        length = struct.unpack_from("<Q", data, ph + 32)[0]
        if length and start + length > len(data):
            raise ValueError("ELF segment is truncated")
        if kind == 1:
            memory = struct.unpack_from("<Q", data, ph + 40)[0]
            if length > memory:
                raise ValueError("invalid load segment memory size")
            loadable = loadable or bool(length)
        if kind == 3:
            if interpreter or data[start:start + length] != b"/lib/ld-musl-aarch64.so.1\0":
                raise ValueError("expected the target's single musl loader")
            interpreter = True
    if not interpreter or not loadable:
        raise ValueError("dynamic musl interpreter or loadable segment not found")


def archive(entries):
    """Entries are (relative POSIX path, bytes or None for directory, mode)."""
    raw = io.BytesIO()
    with tarfile.open(fileobj=raw, mode="w", format=tarfile.GNU_FORMAT) as tar:
        for name, data, mode in sorted(entries):
            info = tarfile.TarInfo("./" + name)
            info.uid = info.gid = 0
            info.uname = info.gname = "root"
            info.mode = mode
            info.mtime = 0
            if data is None:
                info.type = tarfile.DIRTYPE
                tar.addfile(info)
            else:
                info.size = len(data)
                tar.addfile(info, io.BytesIO(data))
    return gzip.compress(raw.getvalue(), compresslevel=9, mtime=0)


def build(binary, output, version="0.2.0-1"):
    data = binary.read_bytes()
    validate_binary(data)
    if not re.fullmatch(r"[0-9][A-Za-z0-9.+~_-]*", version):
        raise ValueError("invalid package version")
    files = Path(__file__).parent / "vohive-sms-forward" / "files"
    root = Path(__file__).resolve().parent.parent
    payload = archive([
        ("usr", None, 0o755), ("usr/bin", None, 0o755),
        ("usr/share", None, 0o755), ("usr/share/licenses", None, 0o755),
        ("usr/share/licenses/vohive-sms-forward", None, 0o755),
        ("usr/share/licenses/vohive-sms-forward/LICENSE", (root / "LICENSE").read_bytes(), 0o644),
        ("usr/bin/vohive-sms-forward", data, 0o755),
        ("etc", None, 0o755), ("etc/config", None, 0o755),
        ("etc/init.d", None, 0o755), ("etc/vohive-sms-forward", None, 0o700),
        ("etc/init.d/vohive-sms-forward", (files / "vohive-sms-forward.init").read_bytes(), 0o755),
        ("etc/config/vohive-sms-forward", (files / "vohive-sms-forward.config").read_bytes(), 0o600),
        ("lib", None, 0o755), ("lib/upgrade", None, 0o755), ("lib/upgrade/keep.d", None, 0o755),
        ("lib/upgrade/keep.d/vohive-sms-forward", b"/etc/config/vohive-sms-forward\n/etc/vohive-sms-forward/\n", 0o644),
    ])
    control = (
        "Package: vohive-sms-forward\n"
        f"Version: {version}\n"
        "Architecture: aarch64_generic\n"
        "Depends: libc, libpthread, libusb-1.0-0, ca-bundle\n"
        "Section: net\nPriority: optional\nLicense: MIT\n"
        "Source: https://github.com/ABreadTree/vohive-sms-forward\n"
        f"Installed-Size: {len(gzip.decompress(payload))}\n"
        "Description: SMS-only USB gateway with Bark and Telegram forwarding\n"
        " Preserves the router network; service remains disabled until configured.\n"
    ).encode()
    prerm = b'''#!/bin/sh
[ -n "$IPKG_INSTROOT" ] && exit 0
if [ -x /etc/init.d/vohive-sms-forward ]; then
    /etc/init.d/vohive-sms-forward stop || true
    if [ "$1" = remove ]; then
        /etc/init.d/vohive-sms-forward disable || true
    fi
fi
exit 0
'''
    controls = archive([
        ("control", control, 0o644),
        ("conffiles", b"/etc/config/vohive-sms-forward\n", 0o644),
        ("prerm", prerm, 0o755),
    ])
    result = archive([
        ("debian-binary", b"2.0\n", 0o644),
        ("control.tar.gz", controls, 0o644),
        ("data.tar.gz", payload, 0o644),
    ])
    output.mkdir(parents=True, exist_ok=True)
    path = output / f"vohive-sms-forward_{version}_aarch64_generic.ipk"
    temp = path.with_suffix(".ipk.tmp")
    try:
        temp.write_bytes(result)
        temp.replace(path)
    finally:
        temp.unlink(missing_ok=True)
    return {"path": str(path.resolve()), "sha256": hashlib.sha256(result).hexdigest(), "size": len(result)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--version", default="0.2.0-1")
    args = parser.parse_args()
    try:
        result = build(args.binary, args.output, args.version)
    except (OSError, ValueError, struct.error) as error:
        parser.error(f"invalid ARM64/musl binary or package input: {error}")
    print(json.dumps(result, sort_keys=True))


if __name__ == "__main__":
    main()
