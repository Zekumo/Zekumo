#!/bin/sh
set -eu

if [ "$#" -ne 1 ]; then
  printf '%s\n' "usage: ROLLBACK.sh TARGET_COPY" >&2
  exit 2
fi

target=$1
artifact_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
pristine="$artifact_dir/PRISTINE_FILE"

if [ ! -f "$pristine" ]; then
  printf '%s\n' "missing pristine sibling: $pristine" >&2
  exit 1
fi

cp "$pristine" "$target"
