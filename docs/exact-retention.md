# Exact cohort retention and daily activity

The original D1/D7/D30 calculation used only players.last_login_at > registration
and therefore measured cumulative return rather than activity on day 1, 7 or 30.
Migration 0026 adds durable player/day/timezone facts and explicit measurement
provenance. No unavailable historical daily activity is fabricated.

Authenticated player activity, including login, records a game-scoped daily fact.
A Redis short claim avoids writing PostgreSQL for every request; successful writes
extend the deduplication marker. A failed write removes the claim for retry, and a
process crash before writing releases the claim after 30 seconds. Activity facts
are retained for 100 days, supporting the existing 90-day UI range. This remains
operational analytics, not a financial or exactly-once event ledger: an outage
with no later player activity can still leave an unrecorded day.

The hourly worker now calculates:
- D1/D7/D30: a cohort member active on exactly registration day +1/+7/+30.
- returned7: a cohort member active at least once on days +1 through +7.
- Values finalize only after the entire target day has elapsed.

The registration funnel uses returned7, not exact-day D7. Registration and first
login remain the same platform event. The workspace dashboard uses the most
recent complete day rather than mixing a partial current day into a final rate.

Tracking is established separately for STATS_TZ. Only cohorts registered after
the first tracking day are eligible for complete measurement. Prior cohorts
return null rates with measurement=insufficient_history; old approximate values
remain physically stored but are not presented as exact history. Keep STATS_TZ
consistent across replicas and stable over time; changes start a new measurement
basis and cannot reconstruct missing prior days for another timezone. Switching
back to a previously used timezone does not fill gaps collected under another.

Existing retention/funnel routes remain. The PRD's query form is also supported:
GET /admin/api/games/{id}/stats?metric=retention (or metric=funnel).
The console explains unknown history and separates exact-day retention from the
within-seven-day funnel. Existing tenant-resource guards cover these endpoints.

Validation in this slice: Go formatting/parsing, console JavaScript syntax and
whitespace checks only. PostgreSQL migration/query execution and backend
compilation remain unrun under the shared-disk restriction.
