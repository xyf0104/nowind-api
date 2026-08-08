#!/usr/bin/env python3
"""Validate and advance XIASS API's bounded three-part release versions."""

from __future__ import annotations

import re
import sys


VERSION_PATTERN = re.compile(r"(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$")
MAX_COMPONENT = 99


def parse(version: str) -> tuple[int, int, int]:
    match = VERSION_PATTERN.fullmatch(version.strip())
    if not match:
        raise ValueError("版本必须为三个非负整数，例如 1.1.2")

    components = tuple(int(component) for component in match.groups())
    if any(component > MAX_COMPONENT for component in components):
        raise ValueError("版本的每一段必须不大于 99")
    return components


def next_version(version: str) -> str:
    major, minor, patch = parse(version)
    if patch < MAX_COMPONENT:
        return f"{major}.{minor}.{patch + 1}"
    if minor < MAX_COMPONENT:
        return f"{major}.{minor + 1}.0"
    return f"{major + 1}.0.0"


def main(arguments: list[str]) -> int:
    if len(arguments) != 2 or arguments[0] not in {"validate", "next"}:
        print("用法: tools/release_version.py {validate|next} <版本>", file=sys.stderr)
        return 2

    command, version = arguments
    try:
        parse(version)
    except ValueError as error:
        print(f"无效版本 {version!r}: {error}", file=sys.stderr)
        return 1

    print(next_version(version) if command == "next" else version.strip())
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
