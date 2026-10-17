import io
import json
from pathlib import Path
import struct
import subprocess
import sys
import tarfile
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("build_ipk.py")


def arm64_elf(interpreter=b"/lib/ld-musl-aarch64.so.1\0", machine=183, load_size=None, elf_type=2):
    data = bytearray(176 + len(interpreter))
    data[:7] = b"\x7fELF\x02\x01\x01"
    struct.pack_into("<HHI", data, 16, elf_type, machine, 1)
    struct.pack_into("<Q", data, 32, 64)
    struct.pack_into("<HHH", data, 52, 64, 56, 2)
    struct.pack_into("<IIQQQQQQ", data, 64, 3, 4, 176, 0, 0, len(interpreter), len(interpreter), 1)
    load_size = len(data) if load_size is None else load_size
    struct.pack_into("<IIQQQQQQ", data, 120, 1, 5, 0, 0, 0, load_size, load_size, 4096)
    data[176:] = interpreter
    return bytes(data)


class IpkBuildTests(unittest.TestCase):
    def invoke(self, root, binary):
        exe = root / "gateway"
        exe.write_bytes(binary)
        return subprocess.run(
            [sys.executable, str(SCRIPT), "--binary", str(exe), "--output", str(root / "out")],
            capture_output=True, text=True,
        )

    def test_package_payload_uses_correct_architecture_dependencies_and_private_modes(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            binary = arm64_elf()
            result = self.invoke(root, binary)
            self.assertEqual(result.returncode, 0, result.stderr)
            metadata = json.loads(result.stdout)
            package = Path(metadata["path"])
            self.assertEqual(package.name, "vohive-sms-forward_0.2.0-1_aarch64_generic.ipk")
            with tarfile.open(package, "r:gz") as outer:
                self.assertEqual(outer.extractfile("./debian-binary").read(), b"2.0\n")
                control_data = outer.extractfile("./control.tar.gz").read()
                payload_data = outer.extractfile("./data.tar.gz").read()
            with tarfile.open(fileobj=io.BytesIO(control_data), mode="r:gz") as control:
                info = control.extractfile("./control").read().decode()
                self.assertIn("Architecture: aarch64_generic\n", info)
                self.assertIn("libusb-1.0-0", info)
                self.assertIn("libc", info)
                self.assertEqual(control.extractfile("./conffiles").read(), b"/etc/config/vohive-sms-forward\n")
                self.assertEqual(control.getmember("./prerm").mode, 0o755)
            with tarfile.open(fileobj=io.BytesIO(payload_data), mode="r:gz") as payload:
                self.assertEqual(payload.extractfile("./usr/bin/vohive-sms-forward").read(), binary)
                self.assertEqual(payload.getmember("./usr/bin/vohive-sms-forward").mode, 0o755)
                self.assertEqual(payload.getmember("./etc/vohive-sms-forward").mode, 0o700)
                self.assertEqual(payload.getmember("./etc/init.d/vohive-sms-forward").mode, 0o755)
                self.assertEqual(payload.getmember("./etc/config/vohive-sms-forward").mode, 0o600)
                self.assertIn(b"/etc/vohive-sms-forward/", payload.extractfile("./lib/upgrade/keep.d/vohive-sms-forward").read())
                for member in payload.getmembers():
                    self.assertEqual((member.uid, member.gid), (0, 0))
                    self.assertNotIn("..", Path(member.name).parts)
            first = package.read_bytes()
            again = self.invoke(root, binary)
            self.assertEqual(again.returncode, 0, again.stderr)
            self.assertEqual(package.read_bytes(), first, "package is not reproducible")

    def test_rejects_wrong_cpu_libc_and_truncated_inputs_before_creating_package(self):
        for binary in [b"not ELF", arm64_elf(machine=62), arm64_elf(b"/lib/ld-linux-aarch64.so.1\0"), arm64_elf()[:80]]:
            with self.subTest(binary=binary[:24]), tempfile.TemporaryDirectory() as d:
                root = Path(d)
                result = self.invoke(root, binary)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("invalid ARM64/musl binary", result.stderr)
                self.assertFalse(list(root.glob("out/*.ipk")))

    def test_rejects_complete_interpreter_but_truncated_load_segment_and_relocatable_input(self):
        for binary in [arm64_elf(load_size=1_000_000), arm64_elf(elf_type=1)]:
            with self.subTest(binary=binary[:24]), tempfile.TemporaryDirectory() as d:
                root = Path(d)
                result = self.invoke(root, binary)
                self.assertNotEqual(result.returncode, 0, "invalid load segment/type was packaged")
                self.assertIn("invalid ARM64/musl binary", result.stderr)
                self.assertFalse(list(root.glob("out/*.ipk")))


if __name__ == "__main__":
    unittest.main()
