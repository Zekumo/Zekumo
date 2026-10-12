# Durable authoritative match records

These records are distinct from realtime rooms. The original next-phases PRD
explicitly excludes room persistence; rooms and matchmaking tickets still vanish
on process restart. This ledger preserves server-authored match rosters, metadata,
status and final results in PostgreSQL (migration 0025).

## Authority and visibility

Only tenant-authorized editors through the admin API, or developer-authored cloud
functions in that game, can create or finalize records. Player API routes are
GET-only and only return matches in which the authenticated player participates.
There is no client result-submission endpoint. A game function must still validate
its own gameplay rules and evidence; calling this API does not make an unvalidated
score trustworthy. Client-owned WebSocket room events never create authoritative
results automatically.

A result and metadata are visible to ALL participating players and authorized
console readers. Do not put secrets or per-player private data in them. Game
identity is derived from middleware or the executing function, never a player
request field. Every supplied participant must belong to that game.

## Lifecycle and bounds

- Create uses a game-local match_key (1–128 bytes) as the idempotency key.
- Roster: 1–200 distinct existing player UUIDs. Metadata: valid JSON, <=8 KiB.
- Identical create replay returns the existing record. Different metadata or
  roster with the same key returns 409 match_conflict.
- New records are active, revision 1. Finalize requires the expected revision,
  status completed or cancelled, and JSON result <=64 KiB.
- Terminal results are immutable. Exact terminal replay succeeds; conflicting
  results or another status return 409. Finalization advances the revision.
- IDs are decimal strings in JSON to avoid JavaScript precision loss.
- List uses before_id cursor pagination, default 20 and maximum 50. Lists omit
  the roster; detail returns player_ids. A nonempty next_before_id may lead to an
  empty final page if the preceding page was exactly full.
- Records persist until their game is deleted; no automatic retention is applied.
  Deleting a player removes their roster membership and their read access.

## Admin REST

All routes are beneath /admin/api/games/{id}/matches and use the existing
workspace header and game-resource authorization.

- GET collection: viewer, optional before_id and limit.
- POST collection: editor, {match_key, player_ids, metadata}.
- GET /{match_id}: viewer, complete record.
- POST /{match_id}/finish: editor,
  {expected_revision, status:"completed"|"cancelled", result}.

The console has a dedicated Match Records page with cursor navigation, details,
creation, cancellation and final result submission. Read-only users do not get
write controls, and the backend independently enforces roles.

## Player REST and SDKs

GET /v1/matches and GET /v1/matches/{match_id} require a player JWT. Nonparticipants
receive 404 for detail rather than learning whether another match exists.

TypeScript: sdk.matches.list({beforeId,limit}), sdk.matches.get(id).
Go: client.Matches.List(ctx,beforeID,limit), client.Matches.Get(ctx,id).
Neither player SDK offers write methods.

## Cloud functions

```js
// Use a server-derived key and a roster checked against your trusted game state.
const match = mc.matches.create('round-2026-001', [request.player.id], {mode:'solo'});
// Verify game evidence before finalizing; never copy untrusted client scores.
const finalized = mc.matches.complete(match.id, match.revision, {verified:true});
finalized
```

mc.matches.get(id) reads a record in this function's game.
mc.matches.cancel(id,revision,reason) records an immutable cancellation.
Results are not automatically coupled to currency grants or achievements; use
those services' existing idempotency controls when awarding effects.

## Verification limits

For this slice only gofmt parsing, console JavaScript syntax and TypeScript
no-emit typecheck ran. No backend compilation, migration execution, PostgreSQL
integration, browser or comprehensive tests ran because the shared disk is low.
The ledger has no retention worker, dedicated game-server credential protocol,
or atomic cross-service reward settlement; trusted cloud functions/admin APIs
are its write interface.
