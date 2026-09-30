"""Run from the repository root: python3 -m unittest discover -s tests -v."""

import json
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
BINARY = os.environ.get("ENTRA_APPREG_BINARY")


@unittest.skipUnless(BINARY is not None or DOTNET, ".NET 10 SDK is required")
class CliTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if BINARY is not None:
            executable = Path(BINARY)
            if not executable.is_absolute():
                raise ValueError("ENTRA_APPREG_BINARY must be an absolute executable path")
            if not executable.is_file() or not os.access(executable, os.X_OK):
                raise FileNotFoundError(f"ENTRA_APPREG_BINARY is not executable: {BINARY}")
            cls.command = [str(executable)]
        else:
            cls.command = [DOTNET, "run", str(SOURCE), "--"]

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.marker = self.root / "auth-attempted"
        fake_bin = self.root / "bin"
        fake_bin.mkdir()
        # Both implementations authenticate only through this fake Azure CLI.
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
        for key in ["TEST_AZ_ARGUMENTS", "TEST_AZ_TENANT_OUTPUT", "TEST_AZ_TOKEN_OUTPUT"]:
            self.env.pop(key, None)
        # Never inherit operator credential or SDK tenant-selection settings.
        for key in list(self.env):
            if key.upper().startswith("AZURE_"):
                self.env.pop(key)
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
        result = subprocess.run(
            [*self.command, *args],
            cwd=self.root,
            env=self.env,
            capture_output=True,
            text=True,
            timeout=120,
        )
        if BINARY is not None:
            self.assertNotIn("fixture-token-must-not-leak", result.stdout + result.stderr)
        return result

    def test_redirect_urls_require_an_explicit_http_authority(self):
        self.env["TEST_AZ_SUCCESS"] = "1"
        for uri in ["http:example.com", "https:/example.com", "https:\\example.com",
                    "https:///example.com"]:
            with self.subTest(uri=uri):
                result = self.run_cli("--name", "URI check", "--redirect-url", uri)
                self.assertEqual(result.returncode, 2, result.stderr)
                self.assertTrue(self.marker.exists())
                self.assertFalse(list(self.root.glob("*.txt")))

    def assert_local_error(self, result):
        self.assertEqual(result.returncode, 2, result.stderr)
        self.assertFalse(self.marker.exists(), "Invalid arguments reached Azure CLI")
        self.assertFalse(list(self.root.glob("*.txt")), "Invalid arguments wrote a report")

    def assert_authentication_reached(self, result):
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertTrue(self.marker.exists(), "Valid arguments should reach authentication")

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
                self.assert_local_error(self.run_cli(
                    "expose-api", "--appid", value, "--scope-name", "read",
                ))

    def test_scope_cannot_start_with_dot(self):
        self.env["TEST_AZ_SUCCESS"] = "1"
        result = self.run_cli(
            "create", "--name", "Test", "--redirect-urls", "https://example.com/callback",
            "--scope-name", ".read",
        )
        self.assertEqual(result.returncode, 2, result.stderr)
        self.assertTrue(self.marker.exists())
        self.assertFalse(list(self.root.glob("*.txt")))

    def test_single_hyphen_name_remains_a_value(self):
        result = self.run_cli("list", "--name", "-preview")
        self.assert_authentication_reached(result)


    def test_version_precedes_help_and_validation(self):
        for args in [
            ("--version",),
            ("--help", "--version"),
            ("create", "--name", "--version", "--help"),
            ("expose-api", "--appid", "--help", "--version"),
            ("list", "--top", "0", "--help", "--version"),
        ]:
            with self.subTest(args=args):
                result = self.run_cli(*args)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stdout.strip(), "entra-appreg 1.0.0")
                self.assertFalse(self.marker.exists())

    def test_unknown_command_precedes_help_and_version(self):
        for args in [
            ("unknown", "--help"),
            ("unknown", "--version"),
            ("help", "unknown", "--help"),
            ("help", "unknown", "--version"),
        ]:
            with self.subTest(args=args):
                self.assert_local_error(self.run_cli(*args))

    def test_no_arguments_prints_help_without_authentication(self):
        result = self.run_cli()
        self.assert_local_error(result)
        self.assertTrue(result.stdout.strip(), "No arguments should print help")
        self.assertTrue(result.stderr.strip(), "No arguments should explain the error")

    def test_secret_options_are_forbidden_on_read_and_expose_commands(self):
        options = [("--create-secret",)]
        for option in ["--secret-expiry", "--secret-start", "--secret-end"]:
            options.extend((option, value) for value in ["", "custom"])
        for command in [("list",), ("expose-api", "--appid", "existing", "--scope-name", "read")]:
            for option in options:
                with self.subTest(command=command[0], option=option):
                    self.assert_local_error(self.run_cli(*command, *option))

    def test_scope_options_are_forbidden_on_list(self):
        for option in [
            "--scope-name", "--scope-display-name", "--scope-description",
            "--scope-consent", "--scope-user-display-name", "--scope-user-description",
            "--scope-state",
        ]:
            for value in ["", "read"]:
                with self.subTest(option=option, value=value):
                    self.assert_local_error(self.run_cli("list", option, value))

    def test_create_and_list_options_are_forbidden_on_expose(self):
        for option in [
            ("--name", ""), ("--name", "Test"),
            ("--audience", ""), ("--audience", "AzureADMyOrg"),
            ("--top", ""), ("--top", "1"), ("--json",),
        ]:
            with self.subTest(option=option):
                self.assert_local_error(self.run_cli(
                    "expose-api", "--appid", "existing", "--scope-name", "read", *option,
                ))

    def test_failed_list_authentication_does_not_pollute_json_stdout(self):
        result = self.run_cli("list", "--json")
        self.assert_authentication_reached(result)
        self.assertEqual(result.stdout, "")
        self.assertIn("az login", result.stderr)
        self.assertFalse(list(self.root.glob("*.txt")))

    def test_parser_rejects_non_csharp_argument_syntax(self):
        for args in [
            ("list", "--"), ("list", "--name=preview"),
            ("list", "--na", "preview"), ("list", "-j"),
            ("list", "--json=true"), ("create", "--create-secret=false"),
            ("list", "--name", "--unknown", "--name", "valid"),
            ("list", "--name", "valid", "extra"),
        ]:
            with self.subTest(args=args):
                self.assert_local_error(self.run_cli(*args))

    def test_repeated_options_and_literal_values_reach_authentication(self):
        for args in [
            ("list", "--top", "0", "--top", "2", "--json", "--json"),
            ("list", "--name", "old", "--name", "-preview"),
            ("list", "--name", "-x=y"),
            ("list", "--name", "name=value"),
            ("create", "--appid", "", "--appid", "existing"),
            ("create", "--create-secret", "--create-secret"),
        ]:
            with self.subTest(args=args):
                result = self.run_cli(*args)
                self.assert_authentication_reached(result)
                self.assertFalse(list(self.root.glob("*.txt")))

    def test_all_help_topics_bypass_authentication(self):
        for args in [
            ("--help",), ("create", "--help"), ("expose-api", "--help"),
            ("list", "--help"), ("help", "general"), ("help", "create"),
            ("help", "expose-api"), ("help", "list"),
        ]:
            with self.subTest(args=args):
                result = self.run_cli(*args)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stderr, "")
                self.assertFalse(self.marker.exists())
                self.assertFalse(list(self.root.glob("*.txt")))

    def recorded_az_calls(self):
        return [
            json.loads(line)
            for line in (self.root / "az-arguments.jsonl").read_text().splitlines()
        ]

    def assert_graph_token_request(self, call):
        self.assertEqual(call[:2], ["account", "get-access-token"])
        if "--scope" in call:
            self.assertEqual(call[call.index("--scope") + 1],
                             "https://graph.microsoft.com/.default")
        else:
            self.assertIn("--resource", call)
            self.assertEqual(call[call.index("--resource") + 1].rstrip("/"),
                             "https://graph.microsoft.com")

    def test_create_uses_current_login_and_pins_discovered_tenant(self):
        tenant = "11111111-1111-1111-1111-111111111111"
        self.env.update({
            "TEST_AZ_SUCCESS": "1",
            "TEST_AZ_ARGUMENTS": str(self.root / "az-arguments.jsonl"),
            "TEST_AZ_TENANT_OUTPUT": tenant,
        })
        # Missing creation fields fail after successful authentication, before Graph.
        result = self.run_cli("create")
        self.assertEqual(result.returncode, 2, result.stderr)
        calls = self.recorded_az_calls()
        self.assertEqual(len(calls), 2)
        self.assertEqual(calls[0][:2], ["account", "show"])
        self.assert_graph_token_request(calls[1])
        self.assertIn("--tenant", calls[1])
        self.assertEqual(calls[1][calls[1].index("--tenant") + 1], tenant)
        self.assertFalse(list(self.root.glob("*.txt")))

    def test_list_and_expose_use_active_login_without_tenant_discovery(self):
        self.env.update({
            "TEST_AZ_SUCCESS": "1",
            "TEST_AZ_ARGUMENTS": str(self.root / "az-arguments.jsonl"),
            # Stop at authentication before either command can call Graph.
            "TEST_AZ_TOKEN_OUTPUT": "fixture-token-must-not-leak",
        })
        for args in [
            ("list", "--json"),
            ("expose-api", "--appid", "existing", "--scope-name", "read"),
        ]:
            with self.subTest(command=args[0]):
                (self.root / "az-arguments.jsonl").unlink(missing_ok=True)
                result = self.run_cli(*args)
                self.assert_authentication_reached(result)
                self.assertEqual(result.stdout, "")
                calls = self.recorded_az_calls()
                self.assertEqual(len(calls), 1)
                self.assert_graph_token_request(calls[0])
                self.assertNotIn("--tenant", calls[0])
                self.assertFalse(list(self.root.glob("*.txt")))

    def test_malformed_or_empty_token_fails_cleanly(self):
        self.env["TEST_AZ_SUCCESS"] = "1"
        token_outputs = [
            "fixture-token-must-not-leak",
            '{"unexpected": "fixture-token-must-not-leak"}',
        ]
        if BINARY is not None:
            # Rust explicitly rejects empty tokens; do not strengthen C#'s contract.
            token_outputs.append(json.dumps({
                "accessToken": "", "tokenType": "Bearer", "expires_on": 4102444800,
            }))
        for token_output in token_outputs:
            with self.subTest(token_output=token_output):
                self.env["TEST_AZ_TOKEN_OUTPUT"] = token_output
                result = self.run_cli("list", "--json")
                self.assert_authentication_reached(result)
                self.assertEqual(result.stdout, "")
                self.assertIn("az login", result.stderr)
                self.assertFalse(list(self.root.glob("*.txt")))

if __name__ == "__main__":
    unittest.main()
