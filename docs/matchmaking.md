# Matchmaking and room sessions

The realtime gateway now supports a bounded, game-isolated FIFO matcher. It uses
existing player authentication, connection ownership, request correlation,
message-rate limits, room limits and outbound backpressure.

Subscribe to `match.found` before enqueueing, because a match can form immediately.

- `match.join`: `{ "pool": "duel", "players": 2 }`, returns `match.queued`
  with a ticket ID, creation time and expiry. Pool names are 1–64 ASCII letters,
  digits, underscores or hyphens. Group size is 2–16. Only identical pool and
  group-size combinations match. An identical retry returns the current ticket;
  changing options requires cancellation first.
- `match.found`: asynchronous `{ticket_id, room}`. Selected players join the same
  locked room atomically. The oldest ticket owns the room.
- `match.cancel`: idempotently cancel a pending ticket, returns `match.cancelled`.
  A match that already formed is not undone; use room.leave in that case.
- `match.status`: returns `{status, ticket, room}` with status idle, queued or
  in_room. Use it after uncertain responses rather than blindly enqueueing again.
- `match.expired`: asynchronous `{ticket_id}` after 120 seconds.

At most 128 clients may queue per game, inside the existing global 1024-client
bound. Queues are process-local, like rooms: disconnect, replacement connection,
and successful manual create/join remove the ticket. Reconnect does not requeue.
Restarts clear tickets and rooms. This is unranked matchmaking: pool/size are
client-selected and do not prove skill, entitlements, game results or rewards.
Multiple server replicas need sticky routing; cross-node matchmaking is not
implemented. The console room monitor includes queued-player counts.

## Sessions

Room snapshots have `session_state`: lobby, running or finished. The owner can
send `room.start` once from lobby and `room.finish` once from running. Both return
and broadcast `room.session` with a fresh snapshot. Starting locks the room;
new joins and room-setting changes are rejected after start. Finished rooms
reject state updates; members can still exchange messages and leave. Empty rooms
are removed. Ownership still transfers when the owner leaves; the successor may
finish the session. A finished room cannot be restarted; create a fresh room.
These are coordination states, not authoritative game simulation or durable
match records.

TypeScript SDK: joinMatchmaking, cancelMatchmaking, matchmakingStatus,
startSession, finishSession and typed events. Go SDK exposes the corresponding
methods and MatchTicket/MatchStatus/MatchFound structs using its existing event
subscription interface.

Only a focused local matcher/session lifecycle test, compilation, SDK typecheck
and console JavaScript syntax check were run for this slice. No load, network,
expiry-timer, cross-tenant integration or browser end-to-end suite was run.
