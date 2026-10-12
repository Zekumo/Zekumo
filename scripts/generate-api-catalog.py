#!/usr/bin/env python3
"""Regenerate the embedded endpoint catalogue from actual Go route registrations."""
import json
import pathlib
import re

root = pathlib.Path(__file__).resolve().parents[1]
routes = []
for line in (root / 'internal/server/routes.go').read_text().splitlines():
    m = re.search(r'"(GET|POST|PUT|DELETE|PATCH) (/[^" ]*)"', line)
    if not m:
        continue
    method, path = m.groups()
    if not (path.startswith(('/v1/', '/admin/api/', '/sso/api/')) or path in ('/oauth/token', '/oauth/userinfo', '/healthz', '/readyz')):
        continue
    auth, role, workspace = 'public', '', False
    if line.lstrip().startswith('ta('):
        auth, workspace = 'admin', True
        role = re.search(r'", "([a-z]+)"', line).group(1)
    elif line.lstrip().startswith('a('):
        auth = 'admin'
    elif line.lstrip().startswith('p(') or 'rt.player(' in line:
        auth = 'player'
    elif 'rt.role(auth.RoleAccount' in line:
        auth = 'account'
    elif 'rt.role(auth.RoleOAuth' in line:
        auth = 'oauth'
    if path.startswith('/v1/kv/') and method in ('PUT', 'DELETE'):
        auth = 'server-hmac'
    if path == '/v1/ws':
        auth = 'player-websocket'
    routes.append({'method': method, 'path': path, 'auth': auth, 'minimum_role': role, 'workspace_header': workspace})
routes.sort(key=lambda item: (item['path'], item['method']))
if len({(r['method'], r['path']) for r in routes}) != len(routes):
    raise SystemExit('duplicate route in catalogue')
(root / 'web/docs/endpoints.json').write_text(json.dumps({'generated_from': 'internal/server/routes.go', 'endpoints': routes}, ensure_ascii=False, indent=2) + '\n')
print(f'Wrote {len(routes)} registered endpoints')
