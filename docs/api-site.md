# Embedded API documentation

The server now serves /docs/ from its embedded filesystem, with no external
runtime dependencies. The public website links to it. It includes SDK quickstart,
identity/workspace guidance, game workflow examples, known feature boundaries,
and a searchable catalogue of registered HTTP routes with safe request templates.
It does not execute requests, collect tokens or load a remote documentation CDN.

Regenerate the route catalogue after editing routes:

    python3 scripts/generate-api-catalog.py

The generator derives methods, paths, auth categories, minimum tenant roles and
workspace-header requirements from internal/server/routes.go. It rejects duplicate
method/path entries. The current catalogue contains 155 routes. Request templates
explicitly require replacing placeholder bodies; they are not executable examples
of every endpoint's full schema. SDK types and linked repository documents remain
the detailed field reference. This is not a generated OpenAPI schema or an API
playground with live credentials.

Validation: Python generation, JSON parsing, JavaScript syntax and whitespace
checks passed. No server compilation or browser rendering was run under the
current shared-disk restriction.
