#!/bin/sh
# Restores the pre-change state of the MiniCloud Phase 3 admin console.
#
# Usage:
#   ROLLBACK.sh                 # restore into the repo this bundle sits in
#   ROLLBACK.sh TARGET_ROOT     # restore into an explicit checkout
#
# The six edited files are overwritten from PRISTINE_FILE; the three files
# this change introduced are deleted, since "pristine" for them is absence.
# Only paths this change touched are affected.
set -eu

artifact_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
pristine="$artifact_dir/PRISTINE_FILE"

if [ "$#" -gt 1 ]; then
  printf '%s\n' "usage: ROLLBACK.sh [TARGET_ROOT]" >&2
  exit 2
fi

target=${1:-$(CDPATH= cd -- "$artifact_dir/../.." && pwd)}

if [ ! -f "$pristine" ]; then
  printf '%s\n' "missing pristine sibling: $pristine" >&2
  exit 1
fi
if [ ! -d "$target" ]; then
  printf '%s\n' "target is not a directory: $target" >&2
  exit 1
fi

tar -xzf "$pristine" -C "$target"

for f in web/admin/page-currency.js web/admin/page-mail.js web/admin/page-bans.js; do
  rm -f "$target/$f"
done

printf '%s\n' "rolled back Phase 3 console in $target"
