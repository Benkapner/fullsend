#!/usr/bin/env python3
"""Single PostToolUse driver: suppress → unicode → redact.

Claude Code runs matching hooks in parallel and does not pipe stdout, so the
three sanitizers cannot be ordered by settings.json matcher position. This
driver loads each enabled sibling script and applies them in-process.

A stage is enabled when its script file is present next to this driver
(HookFiles omits disabled sanitizers). FULLSEND_POSTTOOL_SKIP may list stage
tokens (suppress, unicode, redact) for tests.
"""

from __future__ import annotations

import importlib.util
import json
import os
import sys
import types
from pathlib import Path

import hook_io

HOOKS_DIR = Path(__file__).resolve().parent
MAX_INPUT_CHARS = hook_io.MAX_INPUT_CHARS

_STAGE_FILES = {
    "suppress": "context_suppress_posttool.py",
    "unicode": "unicode_posttool.py",
    "redact": "secret_redact_posttool.py",
}


def _skip_set() -> set[str]:
    raw = os.environ.get("FULLSEND_POSTTOOL_SKIP", "")
    return {s.strip() for s in raw.split(",") if s.strip()}


def stage_enabled(token: str) -> bool:
    if token in _skip_set():
        return False
    return (HOOKS_DIR / _STAGE_FILES[token]).is_file()


def _load_stage(token: str) -> types.ModuleType | None:
    filename = _STAGE_FILES[token]
    path = HOOKS_DIR / filename
    if not path.is_file():
        return None
    spec = importlib.util.spec_from_file_location(f"posttool_{token}", path)
    if spec is None or spec.loader is None:
        return None
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


def _command(hook_input: dict) -> str:
    if hook_input.get("tool_name") != "Bash":
        return ""
    tool_input = hook_input.get("tool_input", {})
    if isinstance(tool_input, str):
        try:
            tool_input = json.loads(tool_input)
        except (json.JSONDecodeError, TypeError):
            return ""
    if not isinstance(tool_input, dict):
        return ""
    command = tool_input.get("command", "")
    return command if isinstance(command, str) else ""


def main() -> None:
    try:
        raw = sys.stdin.read(MAX_INPUT_CHARS + 1)
        if len(raw) > MAX_INPUT_CHARS:
            raw = raw[:MAX_INPUT_CHARS]
        if not raw.strip():
            sys.exit(0)
        hook_input = json.loads(raw)
    except (json.JSONDecodeError, Exception):
        sys.exit(0)

    if not isinstance(hook_input, dict):
        sys.exit(0)

    original = hook_io.payload(hook_input)
    updated = original
    metadata: dict = {}

    if stage_enabled("suppress"):
        suppress = _load_stage("suppress")
        command = _command(hook_input)
        text = hook_io.scan_text(updated)
        if suppress is not None and command and not text.startswith("Exit code"):
            summary = suppress.try_suppress(command, text)
            if summary is not None:
                suppress.log_suppression(command, summary)
                updated = hook_io.apply_text(updated, summary)
                metadata["context_suppressed"] = True

    unicode_findings: list[dict] = []
    if stage_enabled("unicode"):
        unicode_mod = _load_stage("unicode")
        if unicode_mod is not None:

            def _sanitize(text: str) -> str:
                if not text:
                    return text
                cleaned, findings = unicode_mod.scan_text(text)
                unicode_findings.extend(findings)
                return cleaned

            updated = hook_io.transform_strings(updated, _sanitize)
            if unicode_findings:
                metadata["unicode_findings"] = len(unicode_findings)
                metadata["categories"] = [f["name"] for f in unicode_findings]
                for f in unicode_findings:
                    action = "critical_sanitize" if f["severity"] == "critical" else "sanitize"
                    unicode_mod.log_finding(f["name"], f["severity"], f["detail"], action)

    redact_findings: list[dict] = []
    if stage_enabled("redact"):
        redact_mod = _load_stage("redact")
        if redact_mod is not None:

            def _redact(text: str) -> str:
                if not text:
                    return text
                cleaned, findings = redact_mod.redact_text(text)
                redact_findings.extend(findings)
                return cleaned

            updated = hook_io.transform_strings(updated, _redact)
            if redact_findings:
                metadata["secrets_redacted"] = len(redact_findings)
                metadata["patterns"] = [f["pattern"] for f in redact_findings]
                for f in redact_findings:
                    redact_mod.log_finding(f["pattern"], f"Redacted {f['pattern']}: {f['masked']}")

    if updated == original and not metadata:
        sys.exit(0)

    hook_io.emit_updated(updated, metadata=metadata or None)
    sys.exit(0)


if __name__ == "__main__":
    main()
