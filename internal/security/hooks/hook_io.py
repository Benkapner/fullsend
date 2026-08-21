#!/usr/bin/env python3
"""Shared PostToolUse stdin/stdout protocol (contract v2).

Claude Code sends the tool output as ``tool_response`` (string or structured
object). Adapters and existing tests may still send ``tool_result``. Sanitizers
replace output via ``hookSpecificOutput.updatedToolOutput`` and also write
``tool_result`` (scan text) so sequential adapters can keep reading the v1
field.

``updatedToolOutput`` must match the original value's shape: a string stays a
string; a Bash object keeps ``stdout``/``stderr``/… keys. A bare string
replacement is ignored for built-in Claude Code tools.
"""

from __future__ import annotations

import json
import sys
from collections.abc import Callable
from typing import Any

MAX_INPUT_CHARS = 10 * 1024 * 1024

# Text-bearing keys on structured tool output. stdout is listed first so
# apply_text writes a replacement there and blanks the remaining slots
# (stderr must be cleared on suppress, not left as a second copy).
_TEXT_KEYS = ("stdout", "stderr", "content", "text", "output")


def payload(hook_input: dict[str, Any]) -> Any:
    """Return the tool output: ``tool_response`` preferred, else ``tool_result``."""
    if "tool_response" in hook_input:
        return hook_input["tool_response"]
    return hook_input.get("tool_result")


def scan_text(value: Any) -> str:
    """Flatten every string field in a tool output value for scanning.

    Claude Code Bash ``tool_response`` is ``{stdout, stderr, interrupted, isImage}``
    with stdout always a string (possibly empty). Detection must not stop at
    the first key — a leak only in stderr would otherwise be invisible.
    """
    if value is None:
        return ""
    if isinstance(value, str):
        return value
    if isinstance(value, dict):
        return "".join(scan_text(v) for v in value.values())
    if isinstance(value, list):
        return "".join(scan_text(item) for item in value)
    if isinstance(value, (bool, int, float)):
        return ""
    return json.dumps(value)


def has_text_slot(value: Any) -> bool:
    """True when apply_text can write back without collapsing the shape."""
    if value is None or isinstance(value, str):
        return True
    if isinstance(value, dict):
        return any(isinstance(value.get(key), str) for key in _TEXT_KEYS)
    if isinstance(value, list) and len(value) == 1:
        item = value[0]
        return isinstance(item, dict) and isinstance(item.get("text"), str)
    return False


def apply_text(original: Any, new_text: str) -> Any:
    """Write ``new_text`` back into the original value's text slot(s).

    Structured values without a recognized text key are returned unchanged —
    Claude Code ignores a bare-string ``updatedToolOutput`` for built-in tools.
    When several text keys are present, the first (stdout) gets ``new_text``
    and the rest are blanked so verbose stderr cannot survive a suppress.
    """
    if original is None or isinstance(original, str):
        return new_text
    if isinstance(original, dict):
        out = dict(original)
        wrote = False
        for key in _TEXT_KEYS:
            if isinstance(original.get(key), str):
                out[key] = new_text if not wrote else ""
                wrote = True
        return out if wrote else original
    if isinstance(original, list) and len(original) == 1:
        item = original[0]
        if isinstance(item, dict) and isinstance(item.get("text"), str):
            block = dict(item)
            block["text"] = new_text
            return [block]
    return original


def looks_failed(value: Any, text: str) -> bool:
    """True when output should not be context-suppressed.

    String adapters prefix failures with ``Exit code``. Claude Code's Bash
    object has no such field — ``interrupted`` is the structured equivalent.
    """
    if text.startswith("Exit code"):
        return True
    return isinstance(value, dict) and value.get("interrupted") is True


def transform_strings(value: Any, fn: Callable[[str], str]) -> Any:
    """Apply ``fn`` to every string in a nested JSON-like value."""
    if isinstance(value, str):
        return fn(value)
    if isinstance(value, dict):
        return {k: transform_strings(v, fn) for k, v in value.items()}
    if isinstance(value, list):
        return [transform_strings(v, fn) for v in value]
    return value


def emit_updated(updated: Any, *, metadata: dict[str, Any] | None = None) -> None:
    payload_out: dict[str, Any] = {
        "tool_result": scan_text(updated),
        "hookSpecificOutput": {
            "hookEventName": "PostToolUse",
            "updatedToolOutput": updated,
        },
    }
    if metadata:
        payload_out["metadata"] = metadata
    json.dump(payload_out, sys.stdout)


def emit_block(reason: str, updated: Any | None = None) -> None:
    payload_out: dict[str, Any] = {"decision": "block", "reason": reason}
    if updated is not None:
        payload_out["tool_result"] = scan_text(updated)
        payload_out["hookSpecificOutput"] = {
            "hookEventName": "PostToolUse",
            "updatedToolOutput": updated,
        }
    json.dump(payload_out, sys.stdout)


def redact_canary(value: Any, canary: str) -> Any:
    """Replace case-insensitive canary matches in every string field."""
    needle = canary.strip().lower()
    if not needle:
        return value

    def _redact(text: str) -> str:
        lower = text.lower()
        if needle not in lower:
            return text
        out: list[str] = []
        i = 0
        n = len(needle)
        while i < len(text):
            if lower.startswith(needle, i):
                out.append("[CANARY_REDACTED]")
                i += n
            else:
                out.append(text[i])
                i += 1
        return "".join(out)

    return transform_strings(value, _redact)
