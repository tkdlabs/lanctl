#!/usr/bin/env bash
# Dev start script — builds and runs lanctl-go from the repo.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

cd "$SCRIPT_DIR"
exec go run ./cmd/lanctl/
