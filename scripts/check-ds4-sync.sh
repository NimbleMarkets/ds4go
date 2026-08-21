#!/bin/sh
# Check ds4go's FFI mirrors against an upstream ds4 checkout.
#
# ds4api mirrors C structs that libds4 reads by byte offset, so an upstream
# field insertion breaks the ABI with no compile or load error. The Go layout
# test cannot catch that on its own: its expectations are a recorded snapshot,
# so when ds4.h moves, the Go struct and the numbers stay stale together and
# the test still passes. This script is the part that notices, by recompiling
# the real header and comparing.
#
# Usage:  scripts/check-ds4-sync.sh
# Env:    DS4_SRC=../ds4          upstream ds4 checkout (default: ../ds4)
#         CC=cc                   C compiler used for the offsetof probe
#         DS4_SYNC_STRICT=1       fail instead of skipping when DS4_SRC is absent
#
# Exit:   0 in sync (or skipped), 1 drift found, 2 usage/tooling error.

set -u

# shellcheck disable=SC1007  # empty CDPATH is deliberate, so cd cannot wander
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
DS4_SRC=${DS4_SRC:-"$ROOT/../ds4"}
CC=${CC:-cc}
DS4_SYNC_STRICT=${DS4_SYNC_STRICT:-}

LAYOUT_TEST="$ROOT/ds4api/raw_layout_test.go"
LOADER="$ROOT/ds4api/loader.go"

info() { echo "$*"; }
warn() { echo "$*" >&2; }
die() { echo "error: $*" >&2; exit 2; }

[ -f "$LAYOUT_TEST" ] || die "missing $LAYOUT_TEST"
[ -f "$LOADER" ] || die "missing $LOADER"

if [ ! -f "$DS4_SRC/ds4.h" ]; then
    if [ -n "$DS4_SYNC_STRICT" ]; then
        die "no ds4.h under DS4_SRC=$DS4_SRC"
    fi
    info "skipped: no ds4.h under DS4_SRC=$DS4_SRC"
    info "         set DS4_SRC to an upstream ds4 checkout to run this check."
    exit 0
fi

command -v "$CC" >/dev/null 2>&1 || die "no C compiler ($CC); set CC="

WORK=$(mktemp -d) || die "could not create a temp dir"
trap 'rm -rf "$WORK"' EXIT INT TERM

# --------------------------------------------------------------------------
# Expectations: the struct/field/offset triples the Go layout test pins.
# Parsing them keeps the probe and the test naming the same C fields, so the
# two cannot drift apart independently.
# --------------------------------------------------------------------------
awk '
    /checkOffsets\(t, "/ {
        match($0, /"[a-z0-9_]+"/)
        st = substr($0, RSTART + 1, RLENGTH - 2)
        # expected size is the literal before the []fieldOffset slice
        if (match($0, /, [0-9]+, \[\]fieldOffset/)) {
            sz = substr($0, RSTART, RLENGTH)
            gsub(/[^0-9]/, "", sz)
            print "size", st, sz
        }
        next
    }
    /^[\t ]*\{"[a-z0-9_]+", unsafe\.Offsetof/ {
        match($0, /"[a-z0-9_]+"/)
        f = substr($0, RSTART + 1, RLENGTH - 2)
        # trailing ", N}," is the expected offset
        if (match($0, /, [0-9]+\},?[\t ]*$/)) {
            off = substr($0, RSTART, RLENGTH)
            gsub(/[^0-9]/, "", off)
            if (st != "") print "off", st, f, off
        }
    }
' "$LAYOUT_TEST" > "$WORK/expected.txt"

[ -s "$WORK/expected.txt" ] || die "could not parse expectations from $LAYOUT_TEST"

# --------------------------------------------------------------------------
# Ground truth: compile a probe that reports the same fields via offsetof.
# --------------------------------------------------------------------------
{
    echo '#include <stddef.h>'
    echo '#include <stdio.h>'
    echo '#include "ds4.h"'
    echo 'int main(void) {'
    while read -r kind a b _; do
        case "$kind" in
        size) printf '    printf("size %s %%zu\\n", sizeof(%s));\n' "$a" "$a" ;;
        off) printf '    printf("off %s %s %%zu\\n", offsetof(%s, %s));\n' "$a" "$b" "$a" "$b" ;;
        esac
    done < "$WORK/expected.txt"
    echo '    return 0;'
    echo '}'
} > "$WORK/probe.c"

if ! "$CC" -I"$DS4_SRC" -o "$WORK/probe" "$WORK/probe.c" 2> "$WORK/cc.log"; then
    warn "the offsetof probe did not compile against $DS4_SRC/ds4.h:"
    sed 's/^/    /' "$WORK/cc.log" >&2
    warn ""
    warn "A field named in $LAYOUT_TEST no longer exists upstream, which is"
    warn "itself drift: reconcile the Go struct with the current header."
    exit 1
fi
"$WORK/probe" > "$WORK/actual.txt" || die "the probe failed to run"

# --------------------------------------------------------------------------
# Compare.
# --------------------------------------------------------------------------
status=0
if ! diff -u "$WORK/expected.txt" "$WORK/actual.txt" > "$WORK/layout.diff"; then
    warn "ABI DRIFT: ds4.h no longer matches the pinned layout."
    warn "  - expected (ds4api/raw_layout_test.go)"
    warn "  + actual   ($DS4_SRC/ds4.h)"
    warn ""
    sed 's/^/    /' "$WORK/layout.diff" >&2
    warn ""
    warn "Update the Go struct in ds4api/raw.go AND the numbers in the layout"
    warn "test to match the header. Never adjust the numbers to match Go."
    status=1
else
    info "layout: in sync ($(grep -c '^off' "$WORK/expected.txt") fields across $(grep -c '^size' "$WORK/expected.txt") structs)"
fi

# --------------------------------------------------------------------------
# Symbols: declarations in ds4.h vs the names ds4api registers.
# --------------------------------------------------------------------------
grep -oE '^[a-zA-Z_].*\b(ds4_[a-z0-9_]+)\(' "$DS4_SRC/ds4.h" |
    grep -oE 'ds4_[a-z0-9_]+' | sort -u > "$WORK/declared.txt"
grep -oE '"ds4_[a-z0-9_]+"' "$LOADER" | tr -d '"' | sort -u > "$WORK/bound.txt"

# Symbols registered behind a purego.Dlsym probe are optional: an older libds4
# without them still loads, so their absence upstream is not a break.
grep -oE 'purego\.Dlsym\([^,]*, "ds4_[a-z0-9_]+"' "$LOADER" |
    grep -oE 'ds4_[a-z0-9_]+' | sort -u > "$WORK/optional.txt"
comm -23 "$WORK/bound.txt" "$WORK/optional.txt" > "$WORK/required.txt"

comm -13 "$WORK/declared.txt" "$WORK/required.txt" > "$WORK/gone.txt"
if [ -s "$WORK/gone.txt" ]; then
    warn ""
    warn "REQUIRED SYMBOL NOT DECLARED upstream (registration would panic):"
    sed 's/^/    /' "$WORK/gone.txt" >&2
    status=1
fi

comm -13 "$WORK/declared.txt" "$WORK/optional.txt" > "$WORK/gone_opt.txt"
if [ -s "$WORK/gone_opt.txt" ]; then
    info "optional symbols absent upstream (guarded, so they degrade cleanly):"
    sed 's/^/    /' "$WORK/gone_opt.txt"
fi

new=$(comm -23 "$WORK/declared.txt" "$WORK/bound.txt" |
    grep -cvE '^ds4_(tokens|think_mode|context_memory|session_rewrite_result|test_)$')
info "symbols: $(wc -l < "$WORK/bound.txt" | tr -d ' ') bound, $new declared upstream but unbound (informational)"

exit "$status"
