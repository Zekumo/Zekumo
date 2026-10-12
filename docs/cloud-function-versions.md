# Cloud function versions and safe draft testing

Every saved function now has a monotonically increasing `revision`. PostgreSQL
snapshots code, enabled/public flags and cron interval atomically on every insert
and update, including legacy writers. Existing functions receive an initial
snapshot during migration 0022. The most recent 50 versions per function are
retained; deleting a function deletes its history. These are recovery checkpoints,
not permanent audit records or backups.

The console displays revisions and supports loading a historical snapshot into
the editor, or restoring it as a new live revision after confirmation. Restoring
also restores public visibility and scheduling, which the dialog discloses.

Admin APIs, with existing workspace/game authorization:
- GET `.../functions/{name}/versions`: latest 50 metadata snapshots
- GET `.../functions/{name}/versions/{revision}`: snapshot including code
- POST `.../functions/{name}/versions/{revision}/restore` with
  `{"expected_revision":123}`: publish historical settings as a new revision
- PUT `.../functions/{name}` accepts optional `expected_revision`. Zero means
  create-only; a positive revision means update only that current revision.
  Stale writes return 409 `revision_conflict`. Omitting it retains legacy
  last-writer-wins behavior. The console always sends a precondition.
- POST `.../functions/{name}/preview` with `{"code":"...","body":{}}`:
  execute an unsaved draft under the existing sandbox/timeout/concurrency limits.
  It does not publish code or settings. It DOES have real game-data access and
  can cause effects; the console warns about this. The test player remains null,
  just as in the existing saved-function test endpoint.

Invalid JavaScript cannot be saved through the admin PUT API. Existing saved
function test endpoints remain backward compatible. Player/public invocation
responses include the exact executed `revision`; TypeScript and Go SDK response
types expose it. A draft preview reports revision zero.

Validation for this slice is intentionally focused: compile checks, JavaScript
syntax and a PostgreSQL lifecycle sanity test. Comprehensive concurrency, UI,
migration-upgrade and regression suites have not been run for this slice.

## Persistent shared drafts

Migration 0023 adds one shared draft per function. The console loads this draft
when present; Save Draft stores code and settings without changing the live
function or cron runner. Incomplete JavaScript is allowed in drafts. Publish
validates JavaScript, requires both expected live and draft revisions, and
atomically writes a new live snapshot and deletes precisely that draft. Concurrent
editor changes return 409 instead of being lost. New console-created functions
start disabled. Discard Draft requires the displayed draft revision.

Additional game-scoped admin endpoints:
- GET `.../functions/{name}/draft`: `{draft: object|null}` (viewer)
- PUT `.../functions/{name}/draft`: code/settings and `expected_revision`;
  zero is create-only, positive values update only that draft (editor)
- DELETE `.../functions/{name}/draft`: `expected_revision` (editor)
- POST `.../functions/{name}/publish`: `expected_revision` for live code and
  `draft_revision` for the reviewed draft (editor)

Preview runs the editor buffer, whether or not saved. Existing direct PUT APIs
still publish immediately for backward compatibility. Saving or restoring a live
version does not implicitly discard a separately saved draft. Shared drafts are
not personal workspaces and do not retain their own revision history.

Function invocations now emit bounded structured operational logs containing the
function name, executed revision, duration, outcome and preview marker. Successful
publication also logs its function/revision. No request body, return value or
source code is added to these metadata logs. Existing error logs remain. Inspect
these through the existing tenant-scoped console logs with source `funcs`.

## Persistent scheduled dispatch

Migration 0024 persists next-run timestamps, last-dispatched timestamps and
counts. Cron intervals survive restarts. PostgreSQL row locks and SKIP LOCKED
share dispatch intervals across replicas; the scheduler claims at most 32 jobs
per 10-second tick. Enabling a timer or changing its interval schedules the first
run one interval later; disabling removes the schedule. Code-only publication
preserves the due time. A restart after missed intervals runs once when eligible,
without replaying every missed interval.

The console shows scheduling metadata from viewer-protected GET
`.../functions/{name}/schedule`, returning `{schedule: object|null}`.
Dispatch is not completion: due time advances before execution. Process crashes,
runtime overload or script errors do not retry that interval. Game effects still
need business idempotency keys. Already-claimed invocations may finish using the
claimed code revision after an operator changes or disables the function. This
is bounded, replica-coordinated dispatch, not an exactly-once job system.

A focused PostgreSQL schedule lifecycle sanity test checks claiming, duplicate
suppression, dispatch metadata, unchanged function revision and disabling. No
multi-process failover or scheduler load test was run.
