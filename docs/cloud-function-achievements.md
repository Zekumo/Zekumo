# Achievement rewards from cloud functions

Cloud functions can now award achievements using the game's configured keys:

```js
if (!request.player) throw 'Login required';
const award = mc.achievements.unlock(request.player.id, 'first_win');
({ unlocked: !!award.unlocked_at, progress: award.progress })
```

For progress achievements, use an absolute value calculated from trusted
server-side data:

```js
const uid = request.player.id;
const wins = mc.playerdata.get(uid, 'verified_wins') || 0;
mc.achievements.progress(uid, 'win_10', wins);
```

Both operations write the existing persistent achievement ledger. Progress never
regresses and repeated updates preserve the original unlock timestamp. The first
unlock transition emits the existing achievement.unlocked webhook exactly as
admin-awarded achievements do. Webhook delivery retains its existing retry
semantics; database commits and webhook enqueueing are not one distributed
transaction. Definitions and players must belong to the invoking function's
game. Negative progress is rejected; database internals are not returned to
public function callers. Runtime timeout/concurrency limits still apply.

Create the definition in the Achievements console, save and publish a protected
cloud function, call it using either SDK's existing functions API, then read the
result through the existing player achievements APIs. Do not trust a caller's
claimed wins or score without validating them against authoritative game data.
Public functions and draft previews have request.player=null by default.
Editors may provide a current-game player ID in the console preview form (or
player_id in the preview API body) to test player-dependent functions. The server
verifies that player belongs to this game before exposing their ID/nickname.
Preview writes real data and may award real achievements; the console warning
applies. Player identity cannot be overridden through public/player invocation APIs.

Validation: focused PostgreSQL-backed JavaScript invocation checked durable
unlock, one first-unlock event and cross-game rejection. Backend compilation and
console syntax checks passed; comprehensive/browser tests were not run.
