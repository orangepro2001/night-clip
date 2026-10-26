#!/usr/bin/env bash
# Prints the release identity as Makefile/shell assignments:
#   CB_VERSION_CODE=<commit count>   monotonic, so every new commit is a newer build
#   CB_VERSION_NAME=<VERSION_BASE>.<commit count>
# VERSION_BASE (repo root) holds the major.minor part you bump by hand.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
base="$(tr -d '[:space:]' < "$root/VERSION_BASE")"
code="$(git -C "$root" rev-list --count HEAD)"
echo "CB_VERSION_CODE=$code"
echo "CB_VERSION_NAME=$base.$code"
