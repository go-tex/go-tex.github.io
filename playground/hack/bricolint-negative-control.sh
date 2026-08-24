#!/usr/bin/env bash
# Negative control for the bricolint guard.
#
# Proves the guard actually bites: injecting a synthetic hand-drawn
# painter primitive into an application file must make bricolint exit
# non-zero, and removing it must make it pass again. Run from the
# playground module directory with $BRICOLINT pointing at the installed
# analyzer binary (defaults to `bricolint` on PATH).
#
# Copyright (c) 2026 the go-tex playground authors. BSD-3-Clause.
set -euo pipefail

BRICOLINT="${BRICOLINT:-bricolint}"
TARGET="app.go"
BACKUP="$(mktemp)"
MARK="bricolint-negative-control"
ANCHOR="p := painter.NewPixelPainter(buf, s.w, s.h)"
INJECT="	p.FillRect(toolkit.Rect{X: 0, Y: 0, W: 10, H: 10}, s.theme.Accent) // $MARK synthetic bricolage"

cleanup() { [ -f "$BACKUP" ] && mv "$BACKUP" "$TARGET"; return 0; }
trap cleanup EXIT

run() { go vet -vettool="$BRICOLINT" . >/dev/null 2>&1; }

# 1. The committed tree must be clean.
if ! run; then
  echo "FAIL: bricolint reports a violation on the clean tree" >&2
  exit 1
fi
echo "ok: clean tree passes bricolint"

# 2. Inject a raw painter primitive on the line after the painter is created in
#    Draw. Plain awk keeps the substitution free of regex/quoting hazards.
cp "$TARGET" "$BACKUP"
awk -v anchor="$ANCHOR" -v inject="$INJECT" \
  'BEGIN{done=0} {print} (index($0, anchor) && !done){print inject; done=1}' \
  "$BACKUP" >"$TARGET"
if ! grep -q "$MARK" "$TARGET"; then
  echo "FAIL: could not inject the synthetic primitive" >&2
  exit 1
fi

# 3. bricolint must now fail.
if run; then
  echo "FAIL: bricolint did NOT flag the injected hand-drawn FillRect" >&2
  exit 1
fi
echo "ok: injected p.FillRect is flagged (guard bites)"

# 4. Remove the injection; bricolint must pass again.
mv "$BACKUP" "$TARGET"
if ! run; then
  echo "FAIL: bricolint still reports a violation after removal" >&2
  exit 1
fi
echo "ok: removal restores a clean pass"

echo "PASS: bricolint negative control"
