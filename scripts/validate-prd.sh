#!/usr/bin/env bash
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

passes=0
failures=0
skips=0

pass() {
  printf 'PASS  %s\n' "$1"
  passes=$((passes + 1))
}

fail() {
  printf 'FAIL  %s\n' "$1" >&2
  failures=$((failures + 1))
}

skip() {
  printf 'SKIP  %s\n' "$1"
  skips=$((skips + 1))
}

run() {
  local name="$1"
  shift
  if "$@"; then
    pass "$name"
  else
    fail "$name"
  fi
}

printf '%s\n' '== PRD source/static acceptance =='
run 'PRD source trace' python3 tests/acceptance/prd_static.py

go_bin="${GO_BIN:-go}"
if go_version="$($go_bin version 2>/dev/null)" && [[ "$go_version" == go\ version\ go* ]]; then
  printf '\n== Go checks (%s) ==\n' "$go_version"
  export GOCACHE="${GOCACHE:-/tmp/minicloud-gocache}"
  export GOMODCACHE="${GOMODCACHE:-/tmp/minicloud-gomodcache}"
  run 'server Go tests' "$go_bin" test ./...
  run 'Go SDK tests' bash -c 'cd sdk-go && "$0" test ./...' "$go_bin"
  run 'go vet' "$go_bin" vet ./...
else
  skip "Go tests/vet: '$go_bin version' did not identify a Go toolchain"
fi

printf '\n== JavaScript SDK checks ==\n'
if command -v npm >/dev/null 2>&1; then
  if [[ ! -x sdk/node_modules/.bin/tsc ]]; then
    run 'install SDK build dependency' env npm_config_cache="${NPM_CONFIG_CACHE:-/tmp/minicloud-npm-cache}" \
      npm --prefix sdk install --ignore-scripts --no-package-lock --no-audit --no-fund
  fi
  if [[ -x sdk/node_modules/.bin/tsc ]]; then
    run 'SDK strict ESM/CJS build' npm --prefix sdk run build
    if [[ -f sdk/dist/esm/index.js ]]; then
      run 'SDK HTTP acceptance' node tests/acceptance/sdk-http.mjs
      run 'SDK realtime retry acceptance' node tests/acceptance/sdk-realtime.mjs
      run 'SDK ESM load' node --input-type=module -e \
        'const m = await import("./sdk/dist/esm/index.js"); if (typeof m.MiniCloud !== "function") process.exit(1)'
      run 'SDK CJS load' node -e \
        'const m = require("./sdk/dist/cjs/index.js"); if (typeof m.MiniCloud !== "function") process.exit(1)'
    else
      fail 'SDK build did not produce dist/esm/index.js'
    fi
  fi
else
  skip 'SDK build/acceptance: npm is unavailable'
fi

printf '\n== Admin UI checks ==\n'
run 'admin social UI acceptance' node tests/acceptance/admin-social-ui.mjs

printf '\n== Live single-instance acceptance ==\n'
if [[ -n "${MINICLOUD_URL:-}" && -n "${MINICLOUD_WS:-}" ]]; then
  if go_version="$($go_bin version 2>/dev/null)" && [[ "$go_version" == go\ version\ go* ]]; then
    run 'full HTTP/WebSocket smoke' "$go_bin" run ./cmd/smoketest
  else
    fail 'live smoke requested, but no valid Go toolchain is available'
  fi
else
  skip 'full smoke: set MINICLOUD_URL and MINICLOUD_WS for a real Postgres/Redis-backed server'
fi

printf '\n== Live two-instance WebSocket acceptance ==\n'
if [[ -n "${MINICLOUD_A:-}" && -n "${MINICLOUD_B:-}" ]]; then
  run 'cluster WebSocket acceptance' node tests/acceptance/cluster-ws.mjs
else
  skip 'cluster WS: set MINICLOUD_A/B for two instances sharing Postgres/Redis'
fi

if [[ "${MINICLOUD_REDIS_OUTAGE:-}" == "1" ]]; then
  run 'Redis outage WebSocket acceptance' node tests/acceptance/redis-outage-ws.mjs
else
  skip 'Redis outage WS: set MINICLOUD_REDIS_OUTAGE=1 only for a disposable Redis instance'
fi

printf '\nSummary: %d passed, %d failed, %d skipped\n' "$passes" "$failures" "$skips"
if (( failures > 0 )); then
  exit 1
fi
if (( skips > 0 )); then
  exit 2
fi
