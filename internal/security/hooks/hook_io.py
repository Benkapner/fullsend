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

_TEXT_KEYS = ("stdout", "content", "text", "output")


def payload(hook_input: dict[str, Any]) -> Any:
    """Return the tool output: ``tool_response`` preferred, else ``tool_result``."""
    if "tool_response" in hook_input:
        return hook_input["tool_response"]
    return hook_input.get("tool_result")


def scan_text(value: Any) -> str:
    """Flatten a tool output value to text for scanning."""
    if value is None:
        return ""
    if isinstance(value, str):
        return value
    if isinstance(value, dict):
        for key in _TEXT_KEYS:
            field = value.get(key)
            if isinstance(field, str):
                return field
        return json.dumps(value)
    if isinstance(value, list):
        return "".join(scan_text(item) for item in value)
    return json.dumps(value)


def apply_text(original: Any, new_text: str) -> Any:
    """Write ``new_text`` back into the original value's text slot."""
    if original is None or isinstance(original, str):
        return new_text
    if isinstance(original, dict):
        out = dict(original)
        for key in _TEXT_KEYS:
            if isinstance(original.get(key), str):
                out[key] = new_text
                return out
        return new_text
    if isinstance(original, list) and len(original) == 1:
        item = original[0]
        if isinstance(item, dict) and isinstance(item.get("text"), str):
            block = dict(item)
            block["text"] = new_text
            return [block]
    return new_text


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
    needle = canary.lower()

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
