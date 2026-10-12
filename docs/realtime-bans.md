# Revoking live realtime connections on ban

The PRD requires bans to invalidate already-issued access immediately. HTTP
requests already checked the ban cache, but an established WebSocket could keep
sending indefinitely. A successful ban now removes the player's room, queue and
chat memberships and closes the active socket with application code 4003.
Connection registration and revocation use the same initialization mutex and
recheck ban status before registration, covering local upgrade/ban races.

A bounded Redis Pub/Sub notice lets other replicas revoke their local connection
too. Receivers verify the durable active ban and matching game before acting, so
stale notices after an unban are ignored. A 10-second bounded reconciliation query
checks only the current process's connected players, recovering missed Pub/Sub
notices or Redis outages when PostgreSQL is available. This control channel does
not implement cross-instance rooms, room metadata, room broadcasts or duplicate
connection ownership. Those remain separate PRD work.

During Redis errors the HTTP token check consults durable bans; database failure
in that fallback fails closed. Existing cache-hit/miss behavior is otherwise
unchanged. Local revocation follows the durable ban operation; cross-instance
revocation is best-effort immediate with periodic reconciliation, not zero-delay
under network partitions. Already delivered packets cannot be recalled.

Both SDKs emit a banned event and stop automatic reconnection on close code 4003.
The Go SDK exposes ErrBanned through Err(). Applications can explain the ban and
only reconnect explicitly after it has been resolved. A newly attempted HTTP/WS
handshake still receives the existing player_banned authorization response.

Validation: Go formatting/parsing, TypeScript no-emit typecheck and whitespace
checks. No backend compilation, Redis integration, multi-replica or WebSocket
execution tests ran under the shared-disk restriction.
