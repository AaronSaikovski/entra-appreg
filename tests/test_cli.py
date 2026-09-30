"""Run from the repository root: python3 -m unittest discover -s tests -v."""

import os
from pathlib import Path
import shlex
import shutil
import socket
import subprocess
import sys
import tempfile
import unittest


SOURCE = Path(__file__).resolve().parents[1] / "src" / "entra-appreg.cs"
DOTNET = shutil.which("dotnet")


@unittest.skipUnless(DOTNET, ".NET 10 SDK is required")
class CliTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.marker = self.root / "auth-attempted"
        fake_bin = self.root / "bin"
        fake_bin.mkdir()
        # Always use our fixture, never the operator's Azure CLI login.
        fixture = Path(__file__).with_name("fake_az.py")
        if os.name == "nt":
            (fake_bin / "az.cmd").write_text(
                f'@echo off\n"{sys.executable}" "{fixture}" %*\n'
            )
        else:
            az = fake_bin / "az"
            az.write_text(
                f"#!/bin/sh\nexec {shlex.quote(sys.executable)} {shlex.quote(str(fixture))} \"$@\"\n"
            )
            az.chmod(0o700)
        self.env = os.environ | {
            "PATH": str(fake_bin) + os.pathsep + os.environ.get("PATH", ""),
            "AUTH_MARKER": str(self.marker),
            "TEST_AZ_SUCCESS": "0",
        }
        # Reserve a non-listening loopback port: even successful fake authentication
        # must not let the real application contact Graph.
        proxy = socket.socket()
        proxy.bind(("127.0.0.1", 0))
        self.addCleanup(proxy.close)
        proxy_url = f"http://127.0.0.1:{proxy.getsockname()[1]}"
        for key in ["HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"]:
            self.env[key] = self.env[key.lower()] = proxy_url
        self.env["NO_PROXY"] = self.env["no_proxy"] = ""

    def run_cli(self, *args):
        self.marker.unlink(missing_ok=True)
        return subprocess.run(
            [DOTNET, "run", str(SOURCE), "--", *args],
            cwd=self.root,
            env=self.env,
            capture_output=True,
            text=True,
            timeout=120,
        )

    def assert_local_error(self, result):
        self.assertEqual(result.returncode, 2, result.stderr)
        self.assertFalse(self.marker.exists(), "Invalid arguments reached Azure CLI")
        self.assertFalse(list(self.root.glob("*.txt")), "Invalid arguments wrote a report")

    def test_missing_values_do_not_consume_flags(self):
        options = [
            "--appid", "--name", "--redirect-urls", "--redirect-url", "--audience",
            "--scope-name", "--scope-display-name", "--scope-description",
            "--scope-consent", "--scope-user-display-name", "--scope-user-description",
            "--scope-state", "--top", "--secret-expiry", "--secret-start", "--secret-end",
        ]
        for option in options:
            with self.subTest(option=option):
                self.assert_local_error(self.run_cli("list", option, "--json"))
        self.assert_local_error(
            self.run_cli("create", "--name", "--create-secret", "--redirect-urls", "https://example.com/callback")
        )

    def test_empty_redirects_are_still_forbidden_on_other_commands(self):
        for command in [("list",), ("expose-api", "--appid", "existing", "--scope-name", "read")]:
            for option in ["--redirect-url", "--redirect-urls"]:
                for value in ["", " , , "]:
                    with self.subTest(command=command[0], option=option, value=value):
                        self.assert_local_error(self.run_cli(*command, option, value))

    def test_help_precedes_missing_value_validation(self):
        for args in [("--name", "--help"), ("expose-api", "--appid", "--help")]:
            with self.subTest(args=args):
                result = self.run_cli(*args)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn("USAGE", result.stdout)
                self.assertFalse(self.marker.exists())

    def test_list_limit_rejects_zero_without_authentication(self):
        self.assert_local_error(self.run_cli("list", "--top", "0"))

    def test_explicit_blank_appid_does_not_fall_through_to_creation(self):
        for value in ["", " \t "]:
            with self.subTest(value=value):
                self.assert_local_error(self.run_cli(
                    "create", "--appid", value, "--name", "Test",
                    "--redirect-urls", "https://example.com/callback",
                ))

    def test_scope_cannot_start_with_dot(self):
        self.env["TEST_AZ_SUCCESS"] = "1"
        result = self.run_cli(
            "create", "--name", "Test", "--redirect-urls", "https://example.com/callback",
            "--scope-name", ".read",
        )
        self.assertEqual(result.returncode, 2, result.stderr)
        self.assertFalse(list(self.root.glob("*.txt")))

    def test_single_hyphen_name_remains_a_value(self):
        result = self.run_cli("list", "--name", "-preview")
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertTrue(self.marker.exists(), "Valid arguments should reach authentication")


if __name__ == "__main__":
    unittest.main()
