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
