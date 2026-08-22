#!/usr/bin/env python3
"""Unit tests for secret_redact_posttool.py hook."""

import json
import subprocess
import sys
import unittest
from pathlib import Path

HOOK = str(Path(__file__).parent / "secret_redact_posttool.py")


def run_hook(tool_result: str) -> tuple[int, str, str]:
    """Run the hook script and return (exit_code, stdout, stderr)."""
    stdin_raw = json.dumps({"tool_name": "Bash", "tool_result": tool_result})
    proc = subprocess.run(
        [sys.executable, HOOK],
        input=stdin_raw,
        capture_output=True,
        text=True,
        timeout=10,
    )
    return proc.returncode, proc.stdout, proc.stderr


class TestEnvSecretRedaction(unittest.TestCase):
    """Tests for env_secret pattern (case-insensitive, underscore-delimited)."""

    def test_lowercase_env_var(self):
        _, stdout, _ = run_hook("export my_token=s3cr3t_value_here")
        self.assertTrue(stdout, "Expected redaction output")
        result = json.loads(stdout)
        self.assertNotIn("s3cr3t_value_here", result["tool_result"])

    def test_mixed_case_env_var(self):
        _, stdout, _ = run_hook("My_Secret_Key=FAKE0000test_value")
        self.assertTrue(stdout, "Expected redaction output")
        result = json.loads(stdout)
        self.assertNotIn("FAKE0000test_value", result["tool_result"])

    def test_uppercase_env_var(self):
        _, stdout, _ = run_hook("export API_KEY=superSecretValue123")
        self.assertTrue(stdout, "Expected redaction output")
        result = json.loads(stdout)
        self.assertNotIn("superSecretValue123", result["tool_result"])

    def test_monkey_not_matched(self):
        """'monkey' contains 'KEY' but should not trigger env_secret."""
        _, stdout, _ = run_hook("monkey=abcdefghijklmnop")
        # stdout should be empty (no redaction) or not contain env_secret
        if stdout:
            result = json.loads(stdout)
            patterns = result.get("metadata", {}).get("patterns", [])
            self.assertNotIn("env_secret", patterns)

    def test_keyboard_not_matched(self):
        """'keyboard_layout' contains 'KEY' but should not trigger env_secret."""
        _, stdout, _ = run_hook("keyboard_layout=us-international-layout")
        if stdout:
            result = json.loads(stdout)
            patterns = result.get("metadata", {}).get("patterns", [])
            self.assertNotIn("env_secret", patterns)

    def test_authority_not_matched(self):
        """'authority_url' contains 'AUTH' but should not trigger env_secret."""
        _, stdout, _ = run_hook("authority_url=https://login.example.com")
        if stdout:
            result = json.loads(stdout)
            patterns = result.get("metadata", {}).get("patterns", [])
            self.assertNotIn("env_secret", patterns)


class TestJsonSecretRedaction(unittest.TestCase):
    """Tests for json_secret pattern (single/double quotes, substring keys)."""

    def test_double_quoted_json(self):
        _, stdout, _ = run_hook('{"password": "my-super-secret-pass-1234"}')
        self.assertTrue(stdout, "Expected redaction output")
        result = json.loads(stdout)
        self.assertNotIn("my-super-secret-pass-1234", result["tool_result"])

    def test_single_quoted_json(self):
        _, stdout, _ = run_hook("{'api_key': 'not-a-prefix-match-v4lue9'}")
        self.assertTrue(stdout, "Expected redaction output")
        result = json.loads(stdout)
        self.assertNotIn("not-a-prefix-match-v4lue9", result["tool_result"])

    def test_capture_group_selection(self):
        """The last non-None capture group should be used as the secret."""
        _, stdout, _ = run_hook("{'token': 'not-a-prefix-match-v4lue9'}")
        self.assertTrue(stdout, "Expected redaction output")
        result = json.loads(stdout)
        self.assertNotIn("not-a-prefix-match-v4lue9", result["tool_result"])


class TestDbPasswordRedaction(unittest.TestCase):
    """Tests for db_password pattern (embedded @, min length, greedy capture)."""

    def test_simple_db_password(self):
        _, stdout, _ = run_hook("postgres://admin:hunter2secret@db:5432/app")
        self.assertTrue(stdout, "Expected redaction output")
        result = json.loads(stdout)
        self.assertNotIn("hunter2secret", result["tool_result"])

    def test_embedded_at_sign(self):
        _, stdout, _ = run_hook("postgres://user:P@ssw0rd1@host:5432/db")
        self.assertTrue(stdout, "Expected redaction output")
        result = json.loads(stdout)
        self.assertNotIn("P@ssw0rd1", result["tool_result"])

    def test_multiple_at_signs(self):
        """Greedy quantifier should capture the full password up to the last @."""
        _, stdout, _ = run_hook("postgres://user:P@ss@w0rd@host:5432/db")
        self.assertTrue(stdout, "Expected redaction output")
        result = json.loads(stdout)
        self.assertNotIn("P@ss@w0rd", result["tool_result"])

    def test_postgresql_scheme(self):
        """postgresql:// (with ql suffix) should also be matched."""
        _, stdout, _ = run_hook("postgresql://admin:hunter2secret@db:5432/app")
        self.assertTrue(stdout, "Expected redaction output")
        result = json.loads(stdout)
        self.assertNotIn("hunter2secret", result["tool_result"])

    def test_short_password_not_matched(self):
        """Passwords below minimum length (4) should not match."""
        _, stdout, _ = run_hook("postgres://user:abc@host:5432/db")
        if stdout:
            result = json.loads(stdout)
            patterns = result.get("metadata", {}).get("patterns", [])
            self.assertNotIn("db_password", patterns)


class TestClaudeContract(unittest.TestCase):
    def test_tool_response_redacts_and_emits_updated_output(self):
        stdin_raw = json.dumps(
            {"tool_name": "Bash", "tool_response": "export API_KEY=superSecretValue123"}
        )
        proc = subprocess.run(
            [sys.executable, HOOK],
            input=stdin_raw,
            capture_output=True,
            text=True,
            timeout=10,
        )
        self.assertEqual(proc.returncode, 0)
        result = json.loads(proc.stdout)
        self.assertNotIn("superSecretValue123", result["tool_result"])
        self.assertEqual(result["hookSpecificOutput"]["updatedToolOutput"], result["tool_result"])


class TestCodeIsNotASecret(unittest.TestCase):
    """Structural patterns must not rewrite ordinary source, docs or fixtures.

    Regression: ``token = request.headers.authorization`` used to come back as
    ``token = requ...`` — an agent then edits against text that is not on disk.
    """

    def test_source_assignment_expression_untouched(self):
        _, stdout, _ = run_hook(
            "const token = request.headers.authorization;\n"
            "const key = Object.keys(map)[0];\n"
            "const auth = HTTPBasicAuth(user, pw);\n"
            'canary_token = os.environ.get("X", "")\n'
            'run(key="tool_response")\n'
        )
        self.assertEqual(stdout, "")

    def test_quoted_literal_in_source_redacted(self):
        _, stdout, _ = run_hook('password = "S3cr3t-P4ss"\n')
        self.assertTrue(stdout)
        self.assertNotIn("S3cr3t-P4ss", json.loads(stdout)["tool_result"])

    def test_jwt_under_camel_case_key_redacted(self):
        value = "eyJhbGciOiJIUzI1NiJ9.abc123xyz"
        _, stdout, _ = run_hook(f'{{"accessToken": "{value}"}}')
        self.assertTrue(stdout)
        self.assertNotIn(value, json.loads(stdout)["tool_result"])

    def test_weak_names_need_credential_shaped_values(self):
        _, stdout, _ = run_hook(
            '{"key": "compound-command", "auth": "basic-header-style", "author": "wayne-sun-dev"}'
        )
        self.assertEqual(stdout, "")
        strong_value = "A1b2C3d4E5f6G7h8I9j0K1l2M3n4"  # gitleaks:allow
        _, stdout, _ = run_hook(f'{{"key": "{strong_value}"}}')
        self.assertTrue(stdout)

    def test_fixture_phrases_untouched(self):
        _, stdout, _ = run_hook(
            'DispatchSecret: "test-secret"\n'
            'Token: "ghs_policy_token"\n'
            'AccessToken: "cached-token"\n'
            'NextPageToken: "page-2-token"\n'
        )
        self.assertEqual(stdout, "")

    def test_names_about_a_secret_untouched(self):
        _, stdout, _ = run_hook(
            "TOKEN_URL=https://example.com/oauth/token\n"
            "KEY_ID=abcd1234efgh\n"
            "PUBLIC_KEY=MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8A\n"
            "GOOGLE_APPLICATION_CREDENTIALS=fullsend-local-credentials.json\n"
            'tokenUrl: "https://x.example/oauth"\n'
        )
        self.assertEqual(stdout, "")

    def test_env_style_values_redacted(self):
        env_lines = "DB_PASSWORD=Tr0ub4dor3xyz\nexport API_KEY=supersecretvalue\n"  # gitleaks:allow
        _, stdout, _ = run_hook(env_lines)
        self.assertTrue(stdout)
        result = json.loads(stdout)
        self.assertNotIn("Tr0ub4dor3xyz", result["tool_result"])
        self.assertNotIn("supersecretvalue", result["tool_result"])
        self.assertEqual(result["metadata"]["patterns"], ["env_secret", "env_secret"])

    def test_placeholders_untouched(self):
        _, stdout, _ = run_hook(
            "export GH_TOKEN=<your-token>\nPASSWORD=changeme\nSECRET=${SECRET}\n"
            "postgres://user:password@host/db\n"
        )
        self.assertEqual(stdout, "")


if __name__ == "__main__":
    unittest.main()
