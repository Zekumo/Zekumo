# Player blocks and realtime visibility

This closes the next-phases PRD requirement that blocked players' room messages
be invisible to the blocker. The existing persistent player_blocks table is
reused; no migration is needed.

- POST /v1/friends/block accepts target_player_id, as before. The block now
  atomically removes friendship and outstanding requests, instead of doing three
  separate mutations. Every participant must belong to the token's game.
- GET /v1/friends/blocks returns {blocks,limit}; each entry has player_id,
  nickname and created_at. Maximum 1024 outgoing blocks per player.
- DELETE /v1/friends/blocks/{player_id} idempotently removes this player's block.
  It does not recreate friendship or restore old requests.
- TS SDK: friends.blocks(), friends.unblock(playerId).
- Go SDK: Friends.Blocks(ctx), Friends.Unblock(ctx,playerID).

New room.msg and room.state broadcasts from blocked senders are withheld from
the blocker. Room snapshots (get, join, update, ready, session and matchmaking)
redact those senders' stored state. Roster identity and room-governance events
remain visible, because everyone still shares the room. Blocking is directional;
it does not kick either player. Already delivered or queued messages cannot be
recalled. Matchmaking avoids grouping pairs when either has blocked the other.

The realtime connection loads persisted preferences before registration, under a
separate initialization mutex; database work never holds the room hub mutex.
Local block/unblock completion refreshes an active connection from persisted
state, avoiding stale ordering between concurrent block/unblock requests. A
refresh failure disconnects that connection instead of keeping stale visibility.
This cache follows the existing single-process room model; changes submitted to
a different replica are not pushed to already-connected sockets here. Reconnect
reloads preferences. Redis cross-node realtime remains a separate PRD item.

The 1024 limit bounds per-connection memory. Legacy accounts above this limit
cannot initialize realtime block preferences until brought under the limit.
Chat-channel history, lobby membership and public metadata are not hidden by this
room-message preference; it is not an anonymity or global content-erasure tool.

Only Go formatting/parsing, TypeScript no-emit typecheck and whitespace checks
were run for this slice. Backend compilation, database/concurrency and WebSocket
integration checks remain unrun under the current disk restriction.
