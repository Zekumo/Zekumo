# MiniCloud PRD acceptance matrix

This matrix traces `prd-next-phases.html` against source snapshot
`5d3c66d590eb44d939a26cc845fda47b13de33b2`. It deliberately separates
source/build evidence from live integration evidence. A route, schema, or mock
test is not recorded as proof that a production-like Postgres/Redis deployment
works.

## Required verification levels

| Level | Evidence | Meaning |
| --- | --- | --- |
| S | Source trace | Route, handler, repository code, migration, SDK method, and smoke phase exist |
| U | Unit/contract | Focused Go or Node tests pass with real language runtimes |
| I | Single-instance integration | Server, Postgres, and Redis run together and `cmd/smoketest` completes |
| C | Cluster integration | Two server instances share Postgres/Redis; WebSocket clients span instances |
| M | Migration compatibility | A populated source schema at migration 0019 upgrades through 0020/0021, then restarts without replay |

## Phase 2: social layer

| Requirement | Production trace | Acceptance criteria | Level/status |
| --- | --- | --- | --- |
| Friend request send/accept/decline/withdraw | `internal/friends`, `internal/repo/friends.go`, `internal/server/routes.go`, `0009_friends.sql`, `0021_social_integrity.sql` | Only the recipient accepts; either party can end a pending request; a declined/withdrawn pair can later retry; reverse simultaneous requests do not leave a stale pending row | S/I/M PASS |
| Friend list and online state | `friends.Handler.List`, `realtime.Hub.IsOnline` | Same-game friends only; online changes after WS connect/disconnect within the documented heartbeat window | S/I/C PASS |
| Friends-only leaderboard | `leaderboard.Handler.Top`, `Friendships.IDs` | `scope=friends` contains the caller and accepted friends, never unrelated players or another game | S/I PASS |
| Configurable friend limit | `games.friend_limit`, admin route, `0019_friend_limit.sql` | Range 1–10000; default 200; enforced for sender and recipient without a concurrency overrun | S/M PASS; contention stress pending |
| Blocking | `PlayerBlocks`, friend handler, realtime filtering | Either direction prevents new requests; existing friendship is removed; blocked players do not see each other's room messages/state | S/I/C PASS |
| Achievement definitions and visibility | `internal/achievements`, `0010_achievements.sql` | Rarity/type/target validation; hidden locked details are redacted; unlocked details are visible | S/I PASS |
| Idempotent instant/progress unlock | `AchievementUnlocks.UpsertWithTransition` | Repeated calls do not duplicate an unlock or webhook; progress unlocks exactly on target | S/I PASS |
| Achievement delete safety | `AchievementDefs.Delete` | Definition with any unlock returns conflict; unused definition deletes | S/I PASS |
| Announcements | `internal/announcements`, `0011_announcements.sql`, `0018_announcement_channels.sql` | Anonymous incremental polling; expiry; platform/channel targeting; update; soft removal; invalid importance/expiry rejected | S/I PASS |

## Phase 3: operations

| Requirement | Production trace | Acceptance criteria | Level/status |
| --- | --- | --- | --- |
| Currency definitions | `internal/currency`, `0012_currency.sql`, `0020_phase3_hardening.sql` | At most eight per game, unique name, same-game isolation | S/U/M PASS; contention stress pending |
| Idempotent grant/spend ledger | `repo.Wallets.Apply/ApplyTx` | Replay returns `duplicate=true` without a second mutation; conflicting amount returns 409; insufficient funds returns 402 under concurrent spend | S/U/I PASS; contention stress pending |
| Mail targeting and expiry | `internal/mailbox`, `0013_mailbox.sql`, `0020_phase3_hardening.sql` | `all` or at most 1000 valid same-game players; invalid recipient rolls back the whole send; default 30-day expiry; expired mail is unavailable | S/U/I/M PASS |
| Atomic, one-time mail rewards | `Mail.ClaimTx`, wallet transaction | Parallel/repeated claims result in exactly one wallet credit and one claim timestamp | S/U/I PASS for repeat; parallel stress pending |
| Retention and funnel | `internal/stats/retention.go`, `0014_retention.sql`, `0020_phase3_hardening.sql` | Exact local-calendar D1/D7/D30 cohorts; immature values stay null; 7-day return funnel; configured timezone is used consistently | S/U/I/M PASS with seeded day-N cohorts |
| Player bans | `internal/bans`, auth middleware, `0015_bans.sql` | Reason/operator audit; timed/permanent ban; old JWT immediately rejected; login rejected with ban details; unban restores access; webhook emitted once | S/U/I PASS |

## Phase 4: platform extension

| Requirement | Production trace | Acceptance criteria | Level/status |
| --- | --- | --- | --- |
| JS/TS SDK player APIs | `sdk/src/index.ts` | Auth, data, leaderboard, achievements, friends, currency, mailbox, announcements, functions, updates, dialogues, chat, KV, and logs compile with strict TS | S/U PASS; selected HTTP contracts PASS |
| SDK module formats | `sdk/tsconfig*.json`, `sdk/scripts/postbuild.mjs`, package exports | ESM import and CJS require load built artifacts; declarations resolve; browser ESM has no Node-only runtime dependency | U PASS for build + ESM/CJS loads |
| SDK realtime | `RealtimeClient` | Heartbeat; typed events; one active connect attempt; bounded exponential backoff; resubscribe after reconnect; manual close/replaced connection does not reconnect | S/U/C PASS |
| Game KV REST | `internal/kv`, `GameData`, `0017_kv_ttl.sql` | Public player reads; signed server writes/deletes; 64 KiB JSON limit; name validation; ±5 minute replay window; TTL bounds/expiry; pagination; game isolation | S/I PASS |
| WebSocket horizontal scale | `internal/realtime` plus Redis coordination | Room metadata/member/state shared across instances; room/chat events reach remote members; replacement kicks an older connection on another instance; Redis loss rejects new upgrades while existing local clients stay usable | S/C PASS, including disposable Redis outage |
| Data exports | `internal/exports`, `0016_exports.sql` | All/single-player scope; nested JSON; multi-table CSV zip; async lifecycle; storage driver; 24-hour download URL; timeout and restart mark jobs failed | S/U/I PASS on local storage; interrupted S3 pending |

## Compatibility and negative-flow checklist

- PASS: populated migration-0019 database upgraded through 0020/0021 in order
- PASS: restart preserved migration timestamps and did not replay either migration
- PASS: representative cross-game/unknown player, currency, mail, KV, announcement cursor, and export identifiers were rejected
- PASS: grant/spend/unlock retries produced one durable mutation
- PASS: cross-instance same-player WS replacement; HTTP friend/currency/mail concurrency uses transactional locks
- PASS: Go SDK context cancellation and JS SDK replaced/manual-close behavior
- PASS: disposable Redis outage rejected new WS upgrades, preserved existing local room delivery, and failed readiness
- PASS: SDK ESM and CJS load checks from outside the SDK source directory
- PENDING: stress-level concurrent friend, currency, and reward-claim races
- PENDING: forced 10-minute export timeout/interrupted S3 upload and real S3-compatible storage
- PENDING: automatic JS SDK reconnect/resubscribe after Redis is restored

## Reproducible commands

Run all available local gates:

```sh
GO_BIN=/path/to/go scripts/validate-prd.sh
```

When a real MiniCloud deployment is available, also set both endpoints. The
smoke test creates and removes a temporary game and exercises HTTP, WebSocket,
storage downloads, and negative flows.

```sh
MINICLOUD_URL=http://127.0.0.1:8080 \
MINICLOUD_WS=ws://127.0.0.1:8080 \
GO_BIN=/path/to/go scripts/validate-prd.sh
```

Cluster acceptance needs two independently addressed server instances backed
by the same Postgres and Redis. It is a separate gate and must not be inferred
from the single-instance smoke result.

```sh
MINICLOUD_A=http://127.0.0.1:8081 \
MINICLOUD_B=http://127.0.0.1:8082 \
GO_BIN=/path/to/go scripts/validate-prd.sh
```

The disposable Redis outage/recovery gate additionally needs
`MINICLOUD_REDIS_OUTAGE=1` and `REDIS_START_SCRIPT=/path/to/start-test-redis`.

## Final evidence (2026-10-04 UTC)

- Implementation under test: local branch `ci/prd-features-validation`; upstream baseline is GitHub main `5d3c66d590eb44d939a26cc845fda47b13de33b2`
- PASS: official Go 1.26.4 checksum verification, server `go test ./...`, `go test -race ./...`, nested Go SDK tests, and `go vet ./...`
- PASS: PRD source trace and SHA-256 immutability checks for published migrations 0001–0019
- PASS: strict TypeScript ESM/CJS build, declarations, both module load modes, five real loopback HTTP contracts, and deterministic bounded reconnect/resubscribe/backoff-cancel coverage
- PASS: fresh PostgreSQL 16.10 + Redis 8.0.2 apply of migrations 0001–0021 and every `cmd/smoketest` step
- PASS: populated 0019 → 0021 upgrade, backfill/invalidation/tenant constraints, data preservation, and restart without replay
- PASS: seeded Asia/Shanghai D1/D7/D30 result `3|1|2|1` and funnel `3 → 3 → 2`
- PASS: two live server instances using the JS SDK for cross-instance room/member/state/message/list/block/kick behavior
- PASS: live Redis shutdown degraded mode, new-upgrade rejection, readiness failure, restoration, and JS SDK automatic recovery
- PASS: deterministic forced export deadline marks the job failed through a fresh non-canceled status context
- NOT RUN: browser visual/interaction QA for the admin pages because this executor's browser cannot access its loopback server
- NOT RUN: real S3/MinIO interrupted upload path and high-contention load/stress tests
- NOT PUBLISHED: npm package, API documentation site, independent SDK repository, workflow, deployment, or pull request
