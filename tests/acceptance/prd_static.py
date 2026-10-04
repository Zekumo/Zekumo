#!/usr/bin/env python3
"""Source-level PRD trace checks.

These checks catch missing wiring and accidental migration rewrites. They do
not prove runtime behavior; scripts/validate-prd.sh runs language tests and the
real deployment smoke separately.
"""

from __future__ import annotations

import hashlib
import re
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
errors: list[str] = []


def text(path: str) -> str:
    candidate = ROOT / path
    try:
        return candidate.read_text(encoding="utf-8")
    except FileNotFoundError:
        errors.append(f"missing file: {path}")
        return ""


def require(path: str, needles: list[str]) -> None:
    body = text(path)
    for needle in needles:
        if needle not in body:
            errors.append(f"{path}: missing {needle!r}")


def require_tree(paths: list[str], needles: list[str], label: str) -> None:
    body = "\n".join(text(path) for path in paths)
    for needle in needles:
        if needle not in body:
            errors.append(f"{label}: missing {needle!r}")


# Every public/admin API promised by the PRD must remain wired.
require(
    "internal/server/routes.go",
    [
        "POST /v1/friends/request",
        "POST /v1/friends/accept",
        "POST /v1/friends/decline",
        "GET /v1/friends",
        "GET /v1/friends/requests",
        "DELETE /v1/friends/{player_id}",
        "POST /v1/friends/block",
        "GET /v1/achievements",
        "GET /v1/achievements/unlocked",
        "POST /admin/api/games/{id}/achievements",
        "POST /admin/api/achievements/{aid}/unlock",
        "GET /v1/apps/{app_id}/announcements",
        "POST /admin/api/games/{id}/announcements",
        "GET /v1/currency",
        "POST /v1/currency/{cid}/spend",
        "POST /admin/api/games/{id}/currency/{cid}/grant",
        "GET /v1/mailbox",
        "POST /v1/mailbox/{mid}/read",
        "POST /v1/mailbox/{mid}/claim",
        "POST /admin/api/games/{id}/mail",
        "GET /admin/api/games/{id}/stats/retention",
        "GET /admin/api/games/{id}/stats/funnel",
        "POST /admin/api/players/{pid}/ban",
        "POST /admin/api/players/{pid}/unban",
        "GET /v1/kv/{namespace}",
        "GET /v1/kv/{namespace}/{key}",
        "PUT /v1/kv/{namespace}/{key}",
        "DELETE /v1/kv/{namespace}/{key}",
        "POST /admin/api/games/{id}/exports",
        "GET /admin/api/exports/{job_id}",
        "GET /admin/api/games/{id}/exports",
    ],
)


# Applied migrations are immutable. New compatibility work belongs in 0020+.
published_migrations = {
    "0001_core.sql": "f603707bbe66c8aeecd9b0711a39be5495823f042121ab1d815c2cffb9f1997e",
    "0002_releases.sql": "7344dff22ad1831e337e27970513e3ffc30a48c0763f794c079a8ab967a68a03",
    "0003_sso.sql": "9e2fb85d4d9bf05482391272fc56875394106c9308a29ea55c558cd327464d00",
    "0004_oauth.sql": "c3e4453b623932306996f6655cfe2012cd152021412410fb7879e9d973e36ba1",
    "0005_functions.sql": "86af6bd007fe8ae4829e5105acd3f28450ad41649170a25c4eb950d807deebdc",
    "0006_webhooks.sql": "4fab1c7e9e6600bca61753c5fe29ab50d5ef8b3dafe7b1716be0bde58bf4e141",
    "0007_stats.sql": "d4aaf509482f5334aa2841a6905fe3e20a89a99b53e5fad22c9766d01a1b9ad0",
    "0008_logs.sql": "09446e4c313a6742cf83259140d9a38a9119ef3dbd54fe58f20ed20228c04f80",
    "0009_friends.sql": "94aa5b2340284c1fee0331a6d8ea6b819a30f63e51b26d453977336ca2ab2706",
    "0010_achievements.sql": "062b0de1fc52deaf2fb0468081ba539845a5ec5f793451bbf5bf0b13160262ea",
    "0011_announcements.sql": "1b69a9b61774a9a336dcc3c3277b5811028c41b982d39ba88f2e83544284e975",
    "0012_currency.sql": "cf5a14ad2ec50fb1625a9bbf6309ace43a5c421a726c33cfee8988e82ec876c0",
    "0013_mailbox.sql": "df861a2d3775694101d404150807ba5c8d49da1d41f4129d82127e317cbce07b",
    "0014_retention.sql": "b991660d645277cc3f00a8287e473d932f5d8b120a2dbc405bf09777ce990c60",
    "0015_bans.sql": "928cf80c73ca90faa1ff2797895783976e353fc364c2dea4f225e603f1cf5214",
    "0016_exports.sql": "9f76e0826a22fb9ceed3ab46d4b30080520af807159a5276255d2b26add99088",
    "0017_kv_ttl.sql": "112260ad6f72c8078bafe5f9e731ae85e6c565177e958b5e0f2c53e7d8921248",
    "0018_announcement_channels.sql": "a247f085ec43b850b42da2a07def1b4f49ee30423c29e9a2421db0461686018e",
    "0019_friend_limit.sql": "74bd9060d30ead457b2148adcaca48e5824564cd6b6ed6b47f517d6b6a2d6b42",
}
migration_dir = ROOT / "internal/store/migrations"
for name, expected in published_migrations.items():
    path = migration_dir / name
    if not path.exists():
        errors.append(f"missing published migration: {name}")
        continue
    actual = hashlib.sha256(path.read_bytes()).hexdigest()
    if actual != expected:
        errors.append(f"published migration changed: {name} ({actual} != {expected})")

names = sorted(path.name for path in migration_dir.glob("*.sql"))
versions = [int(name.split("_", 1)[0]) for name in names]
if versions != sorted(set(versions)):
    errors.append("migration versions are duplicated or out of order")
if versions and versions != list(range(versions[0], versions[-1] + 1)):
    errors.append("migration version sequence has a gap")


# SDK surface and dual-format packaging.
require(
    "sdk/src/index.ts",
    [
        "class AuthAPI",
        "class PlayerDataAPI",
        "class LeaderboardsAPI",
        "class AchievementsAPI",
        "class FriendsAPI",
        "class CurrencyAPI",
        "class MailboxAPI",
        "class AnnouncementsAPI",
        "class FunctionsAPI",
        "export class RealtimeClient",
        'this.send("ping"',
        "scheduleReconnect",
    ],
)
require(
    "sdk/package.json",
    [
        '"main": "./dist/cjs/index.js"',
        '"module": "./dist/esm/index.js"',
        '"types": "./dist/esm/index.d.ts"',
        '"sideEffects": false',
    ],
)


# The executable smoke owns behavior checks; every PRD phase must stay in its
# ordered phase list and have a source file.
smoke_phases = [
    "friends",
    "achievements",
    "announcements",
    "currency",
    "mailbox",
    "bans",
    "kv",
    "exports",
    "realtime",
]
smoke_main = text("cmd/smoketest/main.go")
for phase in smoke_phases:
    path = f"cmd/smoketest/phase_{phase}.go"
    if not (ROOT / path).is_file():
        errors.append(f"missing smoke phase: {path}")
    function = "phaseKV" if phase == "kv" else "phase" + "".join(part.title() for part in phase.split("_"))
    if function not in smoke_main:
        errors.append(f"cmd/smoketest/main.go: missing {function}")


# Phase 4 clustering must be real Redis coordination, not only an in-memory
# Hub. Keep the source assertion broad enough to allow the implementation to
# be split across files.
realtime_paths = [str(path.relative_to(ROOT)) for path in (ROOT / "internal/realtime").glob("*.go")]
require_tree(
    realtime_paths,
    [
        "github.com/redis/go-redis/v9",
        "room:",
        "kick",
    ],
    "internal/realtime",
)
realtime_text = "\n".join(text(path) for path in realtime_paths)
if not re.search(r"\b(PSubscribe|Subscribe|PubSub)\b", realtime_text):
    errors.append("internal/realtime: no Redis Pub/Sub subscription trace")


if errors:
    for error in errors:
        print(f"ERROR: {error}", file=sys.stderr)
    raise SystemExit(1)
print(f"PRD source trace OK: {len(published_migrations)} published migrations protected")
