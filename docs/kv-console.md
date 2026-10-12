# Tenant KV configuration console

The new Game Configuration page manages the existing persistent game_data
namespace/key store. It is the same data returned by the existing player KV SDK
and modified by HMAC-authenticated server requests; no second store or new
credentials are introduced.

All new admin routes require the workspace header and game-resource permission:
- GET /admin/api/games/{id}/kv: paginated namespaces and live key counts (viewer)
- GET /admin/api/games/{id}/kv/{namespace}: paginated key metadata (viewer)
- GET /admin/api/games/{id}/kv/{namespace}/{key}: value, update and expiry (viewer)
- PUT /admin/api/games/{id}/kv/{namespace}/{key}: {value,ttl_seconds} (editor)
- DELETE /admin/api/games/{id}/kv/{namespace}/{key}: idempotent delete (editor)

Namespace/key names retain the existing 1–64 character letters/digits/underscore/
dot/hyphen format. Values remain limited to 64 KiB of valid JSON. ttl_seconds is
required explicitly: zero means permanent, 1–31536000 sets a new expiry. Expired
keys are excluded from reads and counts and reclaimed by the existing sweeper.
Pagination defaults to 20, is capped at 50, and caps offset at 10000. A full last
page may show Next leading to an empty page.

The console has namespace navigation, key paging, JSON viewing/editing, creation,
explicit TTL controls and deletion confirmation, with workspace-change guards.
Viewer roles have read-only controls; the backend independently enforces roles.
Private namespaces are visible to authorized workspace viewers, but player access
still only permits public or public_ prefixed namespaces. Avoid secrets in public
configuration. Direct cloud-function keys without the namespace/key format are
not reinterpreted or rewritten by this page.

Writes remain last-writer-wins, matching the existing KV API and PRD (which defers
compare-and-swap). Saving edits replaces the current value and resets TTL from
the moment of saving. There is no configuration version history in this slice.

Only gofmt parsing, console JavaScript syntax and whitespace checks were run.
Go compilation, PostgreSQL execution and browser tests remain unrun because of
the shared-disk restriction.
