#!/usr/bin/env bash
# Vendor the ord-ai-n agent kit into a repository, or verify a vendored copy.
#
#   ai/sync.sh [--force] <repo>
#                              Copy this kit's ai/ into <repo>/ai/, record what was copied in
#                              <repo>/ai/.kit-manifest, and regenerate the core block in
#                              <repo>/AGENTS.md from ai/CORE.md. AGENTS.md is created if it is
#                              missing, and gets the markers and the Bindings skeleton if it
#                              lacks them. Of the files the earlier manifest listed that this kit
#                              no longer ships, it deletes only those ai/.kit-retired names.
#                              Finishes by running --check on the result.
#                              Run it from an ord-ai-n checkout. It refuses to overwrite what
#                              it didn't write (an ai/ with no manifest, uncommitted edits
#                              under ai/), or to create AGENTS.md outside a repository's root,
#                              unless --force is given.
#   ai/sync.sh --check <repo>  Change nothing. Exit 1 if <repo>/ai/ differs from its manifest, the
#                              manifest's seal is missing or doesn't match (a hand edit), or
#                              <repo>/AGENTS.md's core block differs from ai/CORE.md. Exit 3 if
#                              only the Bindings are unfinished: a placeholder in the skeleton
#                              (ai/INSTALL.md) has no row, a row is blank or still says TODO,
#                              or there are no rows; or ai/AMENDMENTS.md has entries newer than
#                              the Bindings row "Amendments reconciled | Through A<n>" (or there
#                              is no such row), which an update leaves for the repo to reconcile.
#                              Cheap enough for CI: the drift guard that holds whichever
#                              agent or model made the change.
#                              This is the only mode a vendored copy runs.
#
# The core block is everything between these two lines of AGENTS.md:
#
#   <!-- ord-ai-n:core:begin -->
#   <!-- ord-ai-n:core:end -->
#
# A sync adds them if AGENTS.md has neither: before the first heading after the title, or at
# the end. Move them if the block belongs elsewhere, as long as it stays above the Bindings;
# later syncs leave them where they are.
# Repo-specific values go in AGENTS.md's Bindings section, never in ai/: edits there fail
# --check and are overwritten by the next sync. See ai/README.md.

set -euo pipefail
export LC_ALL=C

BEGIN='<!-- ord-ai-n:core:begin -->'
END='<!-- ord-ai-n:core:end -->'
MANIFEST=ai/.kit-manifest
AMENDMENTS=ai/AMENDMENTS.md
RETIRED=ai/.kit-retired

die() { echo "sync: $*" >&2; exit 1; }
usage() { echo "usage: ai/sync.sh [--check | --force] <repo>" >&2; exit 2; }
sha() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$@"; else shasum -a 256 "$@"; fi; }
# The file paths a manifest lists, one per line.
listed() { grep -v '^#' "$1" | sed 's/^[0-9a-f]*  //' || true; }
# The digest of a manifest's file lines, which the installer records as its seal. The seal is a
# tripwire for a hand edit, not proof of what the kit shipped: anyone can recompute it.
seal_of() { { grep -v '^#' "$1" || true; } | sha | cut -d' ' -f1; }
# Whether a manifest's seal matches its file lines: sealed, unsealed (no seal line), or broken.
manifest_seal() {
  local recorded
  recorded=$(sed -n 's/^# seal: //p' "$1" | tail -n 1)
  if [ -z "$recorded" ]; then echo unsealed
  elif [ "$recorded" = "$(seal_of "$1")" ]; then echo sealed
  else echo broken
  fi
}
# The paths the kit used to ship and has retired, one per line: the only files an update deletes.
retired() { [ ! -f "$1" ] || grep -v '^#' "$1" | grep . || true; }
# Whether a file still has the content the target's manifest recorded for it.
unchanged() {  # $1 = path inside the target
  (cd "$target" && awk -v f="$1" '!/^#/ && substr($0, 67) == f' "$MANIFEST" |
    sha -c --quiet - >/dev/null 2>&1)
}
# Whether every file the target's manifest lists is byte for byte what was installed.
matches_manifest() {
  [ -f "$target/$MANIFEST" ] &&
    (cd "$target" && grep -v '^#' "$MANIFEST" | sha -c --quiet - >/dev/null 2>&1)
}
# The Bindings skeleton in ai/INSTALL.md: what follows its heading, up to the closing fence.
skeleton() {
  [ -f "$1" ] || return 0
  # shellcheck disable=SC2016  # the backticks are literal Markdown, not expansions
  awk '/^## Bindings$/{s=1; next} s && /^```/{exit} s' "$1"
}
# Placeholders that skeleton asks every repository to bind.
skeleton_placeholders() {
  # shellcheck disable=SC2016  # the backticks are literal Markdown, not expansions
  skeleton "$1" | grep -o '^| `<[a-z0-9-]*>`' | sed 's/^| `//; s/`$//' | sort -u || true
}
# Of those, the ones AGENTS.md never mentions after the core block, i.e. in its Bindings.
unbound() {  # $1 = AGENTS.md, $2 = ai/INSTALL.md, $3 = line of the end marker
  local after p
  after=$(tail -n "+$(($3 + 1))" "$1")
  skeleton_placeholders "$2" | while IFS= read -r p; do
    [ -n "$p" ] || continue
    # A pattern match in the shell, not grep: a pipe into grep -q let the writer die of SIGPIPE,
    # which pipefail turned into a row reported missing, and a here-string needs a writable temp
    # directory for large text (always, on bash before 5.1).
    [[ $after == *"\`$p\`"* ]] || echo "$p"
  done
}
# Bindings rows still to fill in: their value is blank or starts with the skeleton's TODO. The
# section runs to the next heading of its level or higher, and finding no rows is not a pass.
unfilled() {  # $1 = AGENTS.md, $2 = line of the end marker
  awk -F'|' -v from="$2" '
    NR <= from { next }
    /^[ \t]*(```|~~~)/ { fence = !fence; next }
    fence { next }
    /^#+[ \t]/ {
      match($0, /^#+/)
      if (level && RLENGTH <= level) level = 0
      if (!level && tolower($0) ~ /bindings/) level = RLENGTH
      next
    }
    level && /^\|/ && ++table > 2 {
      rows++
      if ($3 ~ /^[ \t\r]*(TODO|$)/) print $2
    }
    END { if (!rows) print "(no Bindings rows found)" }' "$1" | sed 's/^ *//; s/ *$//'
}
# Give AGENTS.md what a sync needs: create it if it is missing, and add the markers if it has
# neither. Markers that differ only by surrounding whitespace are straightened. Anything else
# is left for locate_markers to refuse.
place_markers() {
  local counts content
  if [ ! -f "$agents" ]; then
    printf '# AGENTS.md\n\n%s\n%s\n' "$BEGIN" "$END" >"$agents"
    return 0
  fi
  counts=$(awk -v b="$BEGIN" -v e="$END" '
    { t = $0; gsub(/^[ \t]+|[ \t\r]+$/, "", t)
      if (t == b) { nb++; lb = NR }
      if (t == e) { ne++; le = NR } }
    END { print nb + 0, ne + 0, (lb < le) ? "in-order" : "reversed" }' "$agents")
  case "$counts" in
    "1 1 in-order")
      content=$(awk -v b="$BEGIN" -v e="$END" '
        { t = $0; gsub(/^[ \t]+|[ \t\r]+$/, "", t); if (t == b || t == e) print t; else print }
      ' "$agents") ;;
    "0 0 "*)
      # Before the first heading that isn't the opening title; "# x" in a code fence is no heading.
      content=$(awk -v b="$BEGIN" -v e="$END" '
        function put() { if (prev != "") print ""; print b; print e }
        /^[ \t]*(```|~~~)/ { fence = !fence }
        !fence && /^#+[ \t]/ {
          if (!done && (seen || !/^#[ \t]/)) { put(); print ""; done = 1 }
          seen = 1
        }
        { print; prev = $0 }
        END { if (!done) put() }' "$agents") ;;
    *) return 0 ;;
  esac
  # Write through the existing file to keep its permissions.
  [ "$content" = "$(cat "$agents")" ] || printf '%s\n' "$content" >"$agents"
}
# Whether the target is the root of a git working tree.
at_repo_root() {
  [ "$(git -C "$target" rev-parse --is-inside-work-tree 2>/dev/null)" = true ] &&
    [ -z "$(git -C "$target" rev-parse --show-prefix)" ]
}
# Find the two markers, which must be exact, single and in order.
locate_markers() {
  [ -f "$agents" ] || die "$agents not found: the kit is not installed here (ai/INSTALL.md)"
  n_begin=$(grep -cxF "$BEGIN" "$agents" || true)
  n_end=$(grep -cxF "$END" "$agents" || true)
  [ "$n_begin" = 1 ] && [ "$n_end" = 1 ] ||
    die "$agents needs exactly one '$BEGIN' line and one '$END' line, unindented (found $n_begin and $n_end)"
  begin_line=$(grep -nxF "$BEGIN" "$agents" | cut -d: -f1)
  end_line=$(grep -nxF "$END" "$agents" | cut -d: -f1)
  [ "$begin_line" -lt "$end_line" ] || die "the begin marker must come before the end marker in $agents"
}
# Whether AGENTS.md has a Bindings section outside the core block, whatever it is titled.
has_bindings() {
  awk -v b="$begin_line" -v e="$end_line" '
    NR >= b && NR <= e { next }
    /^[ \t]*(```|~~~)/ { fence = !fence; next }
    !fence && /^#+[ \t]/ && tolower($0) ~ /bindings/ { found = 1 }
    END { exit !found }' "$agents"
}
# The highest entry number in an amendments log ("## A<n> " headings); 0 if it has none.
latest_amendment() {
  [ -f "$1" ] || { echo 0; return 0; }
  awk '/^## A[0-9]+ / { n = substr($2, 2) + 0; if (n > m) m = n } END { print m + 0 }' "$1"
}
# Entries out of order in an amendments log: each must be numbered below the one above it, so the
# log runs newest first with every number once.
misnumbered() {
  [ ! -f "$1" ] || awk '/^## A[0-9]+ / {
      n = substr($2, 2) + 0
      if (seen && n >= prev) print "A" n " comes after A" prev
      prev = n; seen = 1
    }' "$1"
}
# The entries numbered above $2, one "A<n> (<date>): <rule>" line each, newest first.
amendments_after() {  # $1 = the amendments log, $2 = the number reconciled through
  [ ! -f "$1" ] ||
    awk -v n="$2" '/^## A[0-9]+ / { if (substr($2, 2) + 0 > n) print substr($0, 4) }' "$1"
}
# The number in the "Amendments reconciled" Bindings row (its first A<n>); "none" if the row
# names no number, and nothing if there is no such row.
reconciled_through() {  # $1 = AGENTS.md, $2 = line of the end marker
  awk -F'|' -v from="$2" '
    NR <= from { next }
    /^[ \t]*(```|~~~)/ { fence = !fence; next }
    fence { next }
    /^\|/ && tolower($2) ~ /amendments reconciled/ {
      if (match($3, /A[0-9]+/)) print substr($3, RSTART + 1, RLENGTH - 1) + 0; else print "none"
      exit
    }' "$1"
}

check=0
force=0
while [ $# -gt 0 ]; do
  case "$1" in
    --check) check=1 ;;
    --force) force=1 ;;
    -*) usage ;;
    *) break ;;
  esac
  shift
done
[ $# -eq 1 ] || usage

kit_ai=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
kit_root=$(dirname "$kit_ai")
target=$(cd "$1" 2>/dev/null && pwd -P) || die "no such directory: $1"
agents="$target/AGENTS.md"

if [ "$check" = 1 ]; then
  locate_markers
  cd "$target"
  [ -f ai/CORE.md ] || die "$target/ai/CORE.md not found: the kit is not vendored here"
  status=0
  unfinished=0
  if ! block_diff=$(sed -n "$((begin_line + 1)),$((end_line - 1))p" AGENTS.md |
                    diff -u -L ai/CORE.md -L 'AGENTS.md core block' ai/CORE.md -); then
    echo "sync: AGENTS.md's core block was edited in place. Put repo-specific values in Bindings;" >&2
    echo "      change the core in ord-ai-n and re-sync. Difference:" >&2
    echo "$block_diff" >&2
    status=1
  fi
  missing=$(unbound AGENTS.md ai/INSTALL.md "$end_line")
  if [ -n "$missing" ]; then
    echo "sync: AGENTS.md's Bindings have no row for (see ai/INSTALL.md step 3):" >&2
    echo "$missing" | sed 's/^/  /' >&2
    unfinished=1
  fi
  todo=$(unfilled AGENTS.md "$end_line")
  if [ -n "$todo" ]; then
    echo "sync: AGENTS.md's Bindings still have rows to fill in (see ai/INSTALL.md step 3):" >&2
    echo "$todo" | sed 's/^/  /' >&2
    unfinished=1
  fi
  # A repeated or out-of-order number would hide an entry from every repository past it.
  bad=$(misnumbered "$AMENDMENTS")
  if [ -n "$bad" ]; then
    echo "sync: $AMENDMENTS must list entries newest first, each number once (its § Numbers):" >&2
    while IFS= read -r line; do echo "  $line"; done <<<"$bad" >&2
    status=1
  fi
  # Rule changes an update brought that the repository's own text may still contradict. They
  # hold the check, like an unfilled row, until the Bindings say they are reconciled.
  latest=$(latest_amendment "$AMENDMENTS")
  if [ "$latest" -gt 0 ]; then
    through=$(reconciled_through AGENTS.md "$end_line")
    case "$through" in
      "") echo "sync: AGENTS.md's Bindings have no 'Amendments reconciled' row, so every amendment counts." >&2
          through=0 ;;
      none) echo "sync: AGENTS.md's 'Amendments reconciled' row names no A<number>, so every amendment counts." >&2
          through=0 ;;
    esac
    if [ "$through" -lt "$latest" ]; then
      echo "sync: rules the kit changed that this repository hasn't reconciled yet ($AMENDMENTS):" >&2
      amendments_after "$AMENDMENTS" "$through" | while IFS= read -r entry; do echo "  $entry"; done >&2
      echo "      Fix what each entry lists under 'Check outside ai/' (ai/INSTALL.md § Update), then set" >&2
      echo "      the Bindings row to: | Amendments reconciled | Through A$latest |" >&2
      unfinished=1
    fi
  fi
  # The kit's own checkout has no manifest; a vendored copy always does.
  if [ -f "$MANIFEST" ]; then
    # This script and a sealed manifest are always written by the same sync, so a missing seal
    # is an edit too.
    seal=$(manifest_seal "$MANIFEST")
    if [ "$seal" != sealed ]; then
      echo "sync: $MANIFEST was edited by hand (its seal is $seal). Only the installer writes it. Move" >&2
      echo "      this repository's own files out of ai/, to where the Bindings' <project-docs> row says" >&2
      echo "      (ai/06), commit the move, and run the install line again (ai/INSTALL.md § Update)." >&2
      status=1
    fi
    if ! out=$(grep -v '^#' "$MANIFEST" | sha -c --quiet - 2>&1); then
      echo "sync: vendored kit files were edited in place; change them in ord-ai-n and re-sync:" >&2
      echo "$out" >&2
      status=1
    fi
    if git rev-parse --git-dir >/dev/null 2>&1; then
      # A tracked file the sync just removed is gone from disk, so it isn't extra. If the manifest
      # still lists it, the hash check above has already failed.
      present=$(git ls-files ai | while IFS= read -r f; do
                  if [ -e "$f" ] && [ "$f" != "$MANIFEST" ]; then echo "$f"; fi
                done)
    else
      present=$(find ai -type f ! -path "$MANIFEST" | sort)
    fi
    extra=$(comm -13 <(listed "$MANIFEST" | sort) <(echo "$present" | sort) | grep . || true)
    if [ -n "$extra" ]; then
      echo "sync: files under ai/ that the kit does not ship:" >&2
      echo "$extra" | sed 's/^/  /' >&2
      echo "      ai/ is the kit's, and an update replaces it. Move them where the Bindings' <project-docs>" >&2
      echo "      row says (ai/06). Don't list them in $MANIFEST: only the installer writes it." >&2
      status=1
    fi
  fi
  [ "$status" = 0 ] || exit "$status"
  [ "$unfinished" = 0 ] || exit 3
  echo "sync: agent kit in sync"
  exit 0
fi

# Refuse to overwrite what this script didn't write. A vendored copy (it has a manifest) only
# checks: syncing from it would copy a stale kit over the target, and dropping a manifest into
# a kit checkout would freeze its file list.
[ ! -f "$kit_root/$MANIFEST" ] ||
  die "$kit_ai is a vendored copy; it only runs --check. Sync from an ord-ai-n checkout."
if [ "$target" != "$kit_root" ] && [ "$force" = 0 ]; then
  # With no AGENTS.md to find, nothing else would stop a run from the wrong directory.
  [ -f "$agents" ] || at_repo_root ||
    die "$target isn't the root of a git repository, so AGENTS.md won't be created here. Run this from the root, or pass --force."
  [ ! -d "$target/ai" ] || [ -f "$target/$MANIFEST" ] ||
    die "$target/ai exists but sync.sh didn't write it, and syncing would overwrite it. Move it, or pass --force."
  if [ -d "$target/ai" ] && git -C "$target" rev-parse --git-dir >/dev/null 2>&1 &&
     [ -n "$(git -C "$target" status --porcelain -- ai)" ] && ! matches_manifest; then
    die "$target/ai has uncommitted edits that syncing would overwrite. Commit or drop them, or pass --force."
  fi
fi

# What to copy, and which kit version it is. install.sh names a downloaded kit through
# ORD_AI_N_SOURCE; a local checkout is named by its commit.
if git -C "$kit_root" rev-parse --git-dir >/dev/null 2>&1; then
  # Tracked plus not-yet-added files: what the kit's next commit would ship.
  files=$(cd "$kit_root" && git ls-files --cached --others --exclude-standard ai |
          while IFS= read -r f; do if [ -f "$f" ]; then echo "$f"; fi; done | sort -u)
  version="ord-ai-n@$(git -C "$kit_root" describe --always)"
  [ -z "$(git -C "$kit_root" status --porcelain -- ai)" ] || version="$version-dirty"
else
  files=$(cd "$kit_root" && find ai -type f | sort)
  version=unknown
fi
version=${ORD_AI_N_SOURCE:-$version}
[ -n "$files" ] || die "no kit files found under $kit_ai"

# Past every refusal: nothing above has written to the target.
place_markers
locate_markers
bindings=
if ! has_bindings; then
  bindings=$(skeleton "$kit_ai/INSTALL.md")
  [ -n "$bindings" ] || die "no Bindings skeleton in $kit_ai/INSTALL.md"
  # New Bindings have no older text to reconcile: start the row at the newest amendment.
  newest=$(latest_amendment "$kit_ai/$(basename "$AMENDMENTS")")
  bindings=${bindings//Through A<n>/Through A$newest}
fi

if [ "$target" != "$kit_root" ]; then
  if [ -f "$target/$MANIFEST" ]; then
    # Files the earlier manifest lists that this kit doesn't ship. Delete one only if the kit says
    # it retired that path and the file is as installed. A line added by hand may name the
    # repository's own file, so anything else is kept, and the closing --check names it.
    gone=$(retired "$kit_ai/$(basename "$RETIRED")")
    comm -23 <(listed "$target/$MANIFEST" | sort) <(echo "$files" | sort) |
      while IFS= read -r f; do
        [ -n "$f" ] && [ -e "$target/$f" ] || continue
        if ! grep -qxF "$f" <<<"$gone"; then
          echo "sync: kept $f: the manifest listed it, but the kit neither ships nor retired it" >&2
        elif ! unchanged "$f"; then
          echo "sync: kept $f: the kit retired it, but it was changed after it was installed" >&2
        else
          rm -f "$target/$f" && echo "sync: removed $f (the kit retired it)"
        fi
      done
  fi
  while IFS= read -r f; do
    mkdir -p "$target/$(dirname "$f")"
    cp -p "$kit_root/$f" "$target/$f"
  done <<<"$files"
  {
    echo "# ord-ai-n agent kit, vendored by ai/sync.sh. Do not edit the files listed here:"
    echo "# change them in ord-ai-n and re-run its installer (ai/INSTALL.md). Verify: ai/sync.sh --check ."
    echo "# Only the installer writes this file. The seal on its last line makes a hand edit fail --check."
    echo "# source: $version"
    (cd "$target" && while IFS= read -r f; do sha "$f"; done <<<"$files")
  } >"$target/$MANIFEST"
  echo "# seal: $(seal_of "$target/$MANIFEST")" >>"$target/$MANIFEST"
fi

# Regenerate the core block. Write through the existing file to keep its permissions.
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
{
  head -n "$begin_line" "$agents"
  cat "$kit_ai/CORE.md"
  sed -n "${end_line}p" "$agents"
  if [ -n "$bindings" ]; then
    printf '\n## Bindings\n%s\n' "$bindings"
    # Keep text that follows the end marker directly out of the table.
    [ -z "$(sed -n "$((end_line + 1))p" "$agents")" ] || echo
  fi
  tail -n "+$((end_line + 1))" "$agents"
} >"$tmp"
cat "$tmp" >"$agents"

echo "sync: $target now carries $version"
# Verify the result the way CI will, with the copy that now lives in the target. Unfinished
# Bindings are the expected state after a first install; anything else is a broken sync.
verified=0
bash "$target/ai/sync.sh" --check "$target" || verified=$?
case "$verified" in
  0) ;;
  3) echo "sync: the kit is in place; finish what is named above (Bindings rows to fill in, amendments to reconcile), then run ai/sync.sh --check ." ;;
  *) die "the result failed its own --check" ;;
esac
case "$version" in
  *-dirty) echo "sync: the kit has uncommitted changes; commit them and re-sync before committing $target" >&2 ;;
esac
