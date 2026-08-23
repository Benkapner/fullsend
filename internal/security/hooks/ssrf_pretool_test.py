"""Tests for ssrf_pretool.py PreToolUse hook."""

from __future__ import annotations

import importlib.util
import os

import pytest

HOOK_PATH = os.path.join(os.path.dirname(__file__), "ssrf_pretool.py")


def _load_hook_module():
    spec = importlib.util.spec_from_file_location("ssrf_pretool", HOOK_PATH)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


@pytest.fixture()
def hook():
    return _load_hook_module()


# ---------------------------------------------------------------------------
# _is_in_text_pattern_context tests
# ---------------------------------------------------------------------------


class TestIsInTextPatternContext:
    """Unit tests for the text-manipulation context detector."""

    def test_sed_pipe_delimiter(self, hook):
        cmd = "sed 's|https://github.com/||'"
        m = list(hook.URL_PATTERN.finditer(cmd))[0]
        assert hook._is_in_text_pattern_context(cmd, m.start())

    def test_sed_slash_delimiter(self, hook):
        # URL won't fully match through escaped slashes, but the prefix
        # detection should still work for any URL-shaped match that does land.
        cmd = "sed 's/https://example.com//'"
        matches = list(hook.URL_PATTERN.finditer(cmd))
        if matches:
            assert hook._is_in_text_pattern_context(cmd, matches[0].start())

    def test_sed_with_flags(self, hook):
        cmd = "sed -e 's|https://github.com/||g'"
        m = list(hook.URL_PATTERN.finditer(cmd))[0]
        assert hook._is_in_text_pattern_context(cmd, m.start())

    def test_sed_in_pipeline(self, hook):
        cmd = "echo \"$URL\" | sed 's|https://github.com/||; s|/issues/.*||'"
        m = list(hook.URL_PATTERN.finditer(cmd))[0]
        assert hook._is_in_text_pattern_context(cmd, m.start())

    def test_sed_second_substitution(self, hook):
        cmd = "sed 's|foo|bar|; s|https://api.example.com/||'"
        m = list(hook.URL_PATTERN.finditer(cmd))[0]
        assert hook._is_in_text_pattern_context(cmd, m.start())

    def test_grep_single_quoted(self, hook):
        cmd = "grep 'https://github.com/owner' file.txt"
        m = list(hook.URL_PATTERN.finditer(cmd))[0]
        assert hook._is_in_text_pattern_context(cmd, m.start())

    def test_grep_double_quoted(self, hook):
        cmd = 'grep "https://github.com/owner" file.txt'
        m = list(hook.URL_PATTERN.finditer(cmd))[0]
        assert hook._is_in_text_pattern_context(cmd, m.start())

    def test_grep_with_flags(self, hook):
        cmd = "grep -rn 'https://github.com/' src/"
        m = list(hook.URL_PATTERN.finditer(cmd))[0]
        assert hook._is_in_text_pattern_context(cmd, m.start())

    def test_egrep_quoted(self, hook):
        cmd = "egrep 'https://example.com/path' logfile"
        m = list(hook.URL_PATTERN.finditer(cmd))[0]
        assert hook._is_in_text_pattern_context(cmd, m.start())

    def test_awk_quoted(self, hook):
        cmd = "awk '/https://example.com/' access.log"
        m = list(hook.URL_PATTERN.finditer(cmd))[0]
        assert hook._is_in_text_pattern_context(cmd, m.start())

    def test_curl_url_not_in_context(self, hook):
        cmd = "curl https://api.github.com/repos/owner/repo"
        m = list(hook.URL_PATTERN.finditer(cmd))[0]
        assert not hook._is_in_text_pattern_context(cmd, m.start())

    def test_wget_url_not_in_context(self, hook):
        cmd = "wget https://example.com/file.tar.gz"
        m = list(hook.URL_PATTERN.finditer(cmd))[0]
        assert not hook._is_in_text_pattern_context(cmd, m.start())

    def test_bare_url_not_in_context(self, hook):
        cmd = "https://example.com/something"
        m = list(hook.URL_PATTERN.finditer(cmd))[0]
        assert not hook._is_in_text_pattern_context(cmd, m.start())

    def test_curl_dns_servers_not_in_context(self, hook):
        """Prefix ending in 's=' (--dns-servers=) must not trigger sed bypass."""
        cmd = "curl --dns-servers=https://169.254.169.254/latest/meta-data/"
        m = list(hook.URL_PATTERN.finditer(cmd))[0]
        assert not hook._is_in_text_pattern_context(cmd, m.start())

    def test_curl_pass_not_in_context(self, hook):
        """Prefix ending in 's=' (--pass=) must not trigger sed bypass."""
        cmd = "curl --pass=https://169.254.169.254/latest/meta-data/"
        m = list(hook.URL_PATTERN.finditer(cmd))[0]
        assert not hook._is_in_text_pattern_context(cmd, m.start())

    def test_variable_assignment_not_in_context(self, hook):
        """Variable assignment like 'process=URL' must not trigger sed bypass."""
        cmd = "process=https://169.254.169.254/latest/meta-data/"
        m = list(hook.URL_PATTERN.finditer(cmd))[0]
        assert not hook._is_in_text_pattern_context(cmd, m.start())

    def test_sed_replacement_url_not_in_context(self, hook):
        """URL in sed replacement field must not be exempt."""
        cmd = "sed 's|items|https://evil.com/payload|'"
        m = list(hook.URL_PATTERN.finditer(cmd))[0]
        assert not hook._is_in_text_pattern_context(cmd, m.start())

    def test_notgrep_not_in_context(self, hook):
        """Binary names ending in 'grep' must not trigger grep bypass."""
        cmd = "notgrep 'https://169.254.169.254/' file.txt"
        m = list(hook.URL_PATTERN.finditer(cmd))[0]
        assert not hook._is_in_text_pattern_context(cmd, m.start())

    def test_myawk_not_in_context(self, hook):
        """Binary names ending in 'awk' must not trigger awk bypass."""
        cmd = "myawk '/https://169.254.169.254/' access.log"
        m = list(hook.URL_PATTERN.finditer(cmd))[0]
        assert not hook._is_in_text_pattern_context(cmd, m.start())

    def test_sed_cross_segment_semicolon_not_in_context(self, hook):
        """sed in one statement must not exempt a URL in a later statement."""
        cmd = "echo sed 's|'; curl https://169.254.169.254/latest/meta-data/"
        m = list(hook.URL_PATTERN.finditer(cmd))[0]
        assert not hook._is_in_text_pattern_context(cmd, m.start())

    def test_sed_cross_segment_and_not_in_context(self, hook):
        """sed in one statement must not exempt a URL after &&."""
        cmd = "echo sed 's/' && curl https://evil.internal/"
        m = list(hook.URL_PATTERN.finditer(cmd))[0]
        assert not hook._is_in_text_pattern_context(cmd, m.start())

    def test_sed_cross_segment_pipe_not_in_context(self, hook):
        """URL in a curl segment after a pipe from a sed-mentioning segment."""
        cmd = "echo sed | curl https://169.254.169.254/latest/meta-data/"
        m = list(hook.URL_PATTERN.finditer(cmd))[0]
        assert not hook._is_in_text_pattern_context(cmd, m.start())

    def test_grep_pipe_to_xargs_curl_not_in_context(self, hook):
        """grep -o URL piped to xargs curl is a network target."""
        cmd = "grep -oP 'https://169.254.169.254/latest/' file | xargs curl"
        m = list(hook.URL_PATTERN.finditer(cmd))[0]
        assert not hook._is_in_text_pattern_context(cmd, m.start())

    def test_grep_pipe_to_wget_not_in_context(self, hook):
        """grep URL piped to wget is a network target."""
        cmd = "grep -o 'https://internal.host/path' log | wget -i -"
        m = list(hook.URL_PATTERN.finditer(cmd))[0]
        assert not hook._is_in_text_pattern_context(cmd, m.start())

    def test_grep_pipe_to_sort_still_in_context(self, hook):
        """grep URL piped to non-network command is still exempt."""
        cmd = "grep 'https://github.com/owner' src/ | sort"
        m = list(hook.URL_PATTERN.finditer(cmd))[0]
        assert hook._is_in_text_pattern_context(cmd, m.start())

    def test_grep_no_pipe_still_in_context(self, hook):
        """grep URL without pipe is still exempt (no downstream sink)."""
        cmd = "grep 'https://github.com/owner' file.txt"
        m = list(hook.URL_PATTERN.finditer(cmd))[0]
        assert hook._is_in_text_pattern_context(cmd, m.start())


# ---------------------------------------------------------------------------
# _extract_network_urls tests
# ---------------------------------------------------------------------------


class TestExtractNetworkUrls:
    """Unit tests for network-relevant URL extraction."""

    def test_sed_url_excluded(self, hook):
        cmd = "sed 's|https://github.com/||'"
        assert hook._extract_network_urls(cmd) == []

    def test_curl_url_included(self, hook):
        cmd = "curl https://api.github.com/repos"
        urls = hook._extract_network_urls(cmd)
        assert urls == ["https://api.github.com/repos"]

    def test_mixed_sed_and_curl(self, hook):
        """sed URL excluded, curl URL still validated."""
        cmd = "echo \"$URL\" | sed 's|https://github.com/||' && curl https://api.github.com/repos"
        urls = hook._extract_network_urls(cmd)
        assert len(urls) == 1
        assert "api.github.com" in urls[0]

    def test_multiple_sed_substitutions(self, hook):
        cmd = "sed 's|https://github.com/||; s|https://example.com/||'"
        assert hook._extract_network_urls(cmd) == []

    def test_no_urls(self, hook):
        cmd = "ls -la /tmp"
        assert hook._extract_network_urls(cmd) == []

    def test_grep_url_excluded(self, hook):
        cmd = "grep 'https://github.com/owner' src/"
        assert hook._extract_network_urls(cmd) == []

    def test_curl_dns_servers_url_included(self, hook):
        """URL after --dns-servers= must not be dropped by sed bypass."""
        cmd = "curl --dns-servers=https://169.254.169.254/latest/meta-data/"
        urls = hook._extract_network_urls(cmd)
        assert len(urls) == 1
        assert "169.254.169.254" in urls[0]

    def test_sed_replacement_url_included(self, hook):
        """URL in sed replacement field is still a candidate for validation."""
        cmd = "sed 's|items|https://evil.com/payload|'"
        urls = hook._extract_network_urls(cmd)
        assert len(urls) == 1
        assert "evil.com" in urls[0]

    def test_sed_cross_segment_url_included(self, hook):
        """sed in one statement must not suppress a URL in a later statement."""
        cmd = "echo sed 's|'; curl https://169.254.169.254/latest/meta-data/"
        urls = hook._extract_network_urls(cmd)
        assert len(urls) == 1
        assert "169.254.169.254" in urls[0]

    def test_grep_pipe_to_xargs_curl_included(self, hook):
        """grep -o URL piped to xargs curl must not be dropped."""
        cmd = "grep -oP 'https://169.254.169.254/latest/' file | xargs curl"
        urls = hook._extract_network_urls(cmd)
        assert len(urls) == 1
        assert "169.254.169.254" in urls[0]


# ---------------------------------------------------------------------------
# process_tool_call integration tests
# ---------------------------------------------------------------------------


class TestProcessToolCallSedPatterns:
    """Verify sed/grep/awk URL patterns are not blocked."""

    def test_sed_url_pattern_not_blocked(self, hook):
        """URL literals inside sed substitution patterns should not trigger SSRF."""
        tool_input = {
            "tool_name": "Bash",
            "tool_input": {
                "command": (
                    "REPO=$(echo \"$URL\" | sed 's|https://github.com/||; s|/issues/.*||')"
                ),
            },
        }
        result = hook.process_tool_call(tool_input)
        assert result is None, f"sed pattern should not be blocked, got: {result}"

    def test_sed_with_pipe_delimiter(self, hook):
        tool_input = {
            "tool_name": "Bash",
            "tool_input": {
                "command": "sed 's|https://github.com/||' file.txt",
            },
        }
        assert hook.process_tool_call(tool_input) is None

    def test_sed_with_e_flag(self, hook):
        tool_input = {
            "tool_name": "Bash",
            "tool_input": {
                "command": "sed -e 's|https://example.com/path||g' input.txt",
            },
        }
        assert hook.process_tool_call(tool_input) is None

    def test_grep_pattern_not_blocked(self, hook):
        tool_input = {
            "tool_name": "Bash",
            "tool_input": {
                "command": "grep 'https://github.com/owner/repo' README.md",
            },
        }
        assert hook.process_tool_call(tool_input) is None

    def test_grep_with_flags_not_blocked(self, hook):
        tool_input = {
            "tool_name": "Bash",
            "tool_input": {
                "command": "grep -rn 'https://example.com/' src/",
            },
        }
        assert hook.process_tool_call(tool_input) is None

    def test_awk_pattern_not_blocked(self, hook):
        tool_input = {
            "tool_name": "Bash",
            "tool_input": {
                "command": "awk '/https://example.com/' access.log",
            },
        }
        assert hook.process_tool_call(tool_input) is None


class TestProcessToolCallSSRFStillBlocked:
    """Verify actual SSRF vectors are still caught."""

    def test_curl_to_metadata_still_blocked(self, hook):
        """Actual SSRF vectors must still be caught."""
        tool_input = {
            "tool_name": "Bash",
            "tool_input": {"command": "curl http://169.254.169.254/latest/meta-data/"},
        }
        result = hook.process_tool_call(tool_input)
        assert result is not None, "curl to metadata endpoint should be blocked"

    def test_curl_to_private_ip_blocked(self, hook):
        tool_input = {
            "tool_name": "Bash",
            "tool_input": {"command": "curl http://192.168.1.1/admin"},
        }
        result = hook.process_tool_call(tool_input)
        assert result is not None

    def test_wget_to_metadata_blocked(self, hook):
        tool_input = {
            "tool_name": "Bash",
            "tool_input": {"command": "wget http://169.254.169.254/latest/meta-data/"},
        }
        result = hook.process_tool_call(tool_input)
        assert result is not None

    def test_webfetch_blocked_scheme(self, hook):
        tool_input = {
            "tool_name": "WebFetch",
            "tool_input": {"url": "file:///etc/passwd"},
        }
        result = hook.process_tool_call(tool_input)
        assert result is not None
        assert "Blocked scheme" in result

    def test_webfetch_private_ip_blocked(self, hook):
        tool_input = {
            "tool_name": "WebFetch",
            "tool_input": {"url": "http://10.0.0.1/internal"},
        }
        result = hook.process_tool_call(tool_input)
        assert result is not None

    def test_webfetch_metadata_blocked(self, hook):
        tool_input = {
            "tool_name": "WebFetch",
            "tool_input": {"url": "http://169.254.169.254/latest/meta-data/"},
        }
        result = hook.process_tool_call(tool_input)
        assert result is not None

    def test_bash_file_scheme_still_blocked(self, hook):
        tool_input = {
            "tool_name": "Bash",
            "tool_input": {"command": "curl file:///etc/shadow"},
        }
        result = hook.process_tool_call(tool_input)
        assert result is not None
        assert "Blocked scheme" in result

    def test_mixed_sed_and_curl_blocks_curl(self, hook):
        """sed URL is skipped but curl URL to private IP is still blocked."""
        tool_input = {
            "tool_name": "Bash",
            "tool_input": {
                "command": (
                    "echo \"$URL\" | sed 's|https://github.com/||' "
                    "&& curl http://169.254.169.254/latest/meta-data/"
                ),
            },
        }
        result = hook.process_tool_call(tool_input)
        assert result is not None
        assert "169.254.169.254" in result

    def test_curl_dns_servers_still_blocked(self, hook):
        """curl --dns-servers=URL must not be bypassed by sed pattern detection."""
        tool_input = {
            "tool_name": "Bash",
            "tool_input": {
                "command": "curl --dns-servers=http://169.254.169.254/latest/meta-data/",
            },
        }
        result = hook.process_tool_call(tool_input)
        assert result is not None, "curl --dns-servers=metadata should be blocked"
        assert "169.254.169.254" in result

    def test_sed_cross_segment_injection_blocked(self, hook):
        """sed in one statement must not suppress SSRF in a later statement."""
        tool_input = {
            "tool_name": "Bash",
            "tool_input": {
                "command": "echo sed 's|'; curl http://169.254.169.254/latest/meta-data/",
            },
        }
        result = hook.process_tool_call(tool_input)
        assert result is not None, "cross-segment sed injection should be blocked"
        assert "169.254.169.254" in result

    def test_sed_cross_segment_and_injection_blocked(self, hook):
        """sed in one statement must not suppress SSRF after &&."""
        tool_input = {
            "tool_name": "Bash",
            "tool_input": {
                "command": "echo sed 's/' && curl http://169.254.169.254/latest/meta-data/",
            },
        }
        result = hook.process_tool_call(tool_input)
        assert result is not None, "cross-segment sed && injection should be blocked"
        assert "169.254.169.254" in result

    def test_grep_pipe_to_xargs_curl_blocked(self, hook):
        """grep -o URL piped to xargs curl must be blocked."""
        tool_input = {
            "tool_name": "Bash",
            "tool_input": {
                "command": ("grep -oP 'http://169.254.169.254/latest/' file | xargs curl"),
            },
        }
        result = hook.process_tool_call(tool_input)
        assert result is not None, "grep -o piped to xargs curl should be blocked"
        assert "169.254.169.254" in result


class TestProcessToolCallWebFetchUnchanged:
    """WebFetch tool calls bypass text-pattern detection entirely."""

    def test_webfetch_url_always_validated(self, hook):
        """WebFetch URLs are always network targets — no text-pattern bypass."""
        tool_input = {
            "tool_name": "WebFetch",
            "tool_input": {"url": "http://192.168.1.1/admin"},
        }
        result = hook.process_tool_call(tool_input)
        assert result is not None
