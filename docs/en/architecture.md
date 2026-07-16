# icloud-relay-lookup — architecture

This document records the *why* behind the design. The *what* is in
[AGENTS.md](../../AGENTS.md) and the package doc comments.

## The problem shape

Apple publishes the complete list of iCloud Private Relay egress ranges as a
single public CSV (~287k rows, ~12MB): CIDR prefix + the geo hints (country,
ISO region, city) for the area that egress serves. The question we answer —
"is this IP a Private Relay egress?" — is a longest-prefix match against that
list, plus relaying Apple's own geo hints on a hit.

Three properties drove the design:

1. **The list is moderately large but changes slowly**, and Apple serves it
   with an `ETag` and `cache-control: max-age=3600`. So: cache aggressively,
   revalidate cheaply, and never fetch per-lookup.
2. **The format is unofficial and unversioned.** Apple documents the URL but
   not the schema; columns could change without notice. So: tolerant parsing
   with an explicit format-change guard, never silently indexing garbage.
3. **No credentials exist anywhere in the system.** Like tor-exit-lookup,
   the endpoint is public — configuration carries no secrets by construction.

## Why an offline store, and why raw CSV + meta.json

Like its siblings asn-lookup and tor-exit-lookup, the tool downloads once and
answers locally: IR and log triage fire hundreds of lookups in bursts, and a
per-lookup network round trip (or rate limit) is exactly what we built these
tools to avoid.

tor-exit-lookup re-serializes its few thousand addresses into a sorted JSON
store. Here that would mean rewriting ~290k rows for no benefit: the
downloaded CSV is already deterministic bytes, already compact, and already
the authoritative representation. So the store is **the CSV verbatim** plus a
small `meta.json` carrying what the CSV cannot: `fetched_at`, the `etag`, the
source URL, and counts. Freshness lives in the record, not the file mtime, so
it survives copies and backups — same rule as the siblings.

The split also makes the 304 path natural: a `Not Modified` revalidation
rewrites only `meta.json` (bumping `fetched_at`), leaving the 12MB CSV
untouched. The corollary: anything caching the parsed list must key on
`meta.json`'s mtime, which is what the MCP server does.

Parsing ~287k rows at load costs on the order of 100ms — irrelevant for a CLI
invocation and amortized to zero for the long-running MCP server.

## Why a map per prefix length (and not a trie or sorted ranges)

The classic answers to longest-prefix match are a radix trie or binary search
over sorted, non-overlapping ranges. Both were rejected for the same reason:
they are more code than the data deserves, and the sorted-ranges variant is
only correct if prefixes never overlap — a property of today's list we'd
rather not depend on.

The observation that decides it: the real list uses only **~19 distinct
prefix lengths** (v4 /24–/32, v6 /40–/64). One hash map per length, keyed by
the query address masked to that length, answers a lookup in at most
#lengths map probes — checked longest-first, so a more specific range wins
and overlap is handled by construction. It is a few dozen lines, obviously
correct, and fast enough that the index build (not the lookup) dominates.

Memory: ~290k map entries of `netip.Addr` → int32 lands in the tens of MB.
Fine for a CLI, fine for a resident MCP server.

## Why the format-change guard is a hard error

`relaylist.Parse` is tolerant per-line (a bad row is counted, not fatal) —
that absorbs cosmetic drift. But if **more than 10% of non-empty rows** fail
to parse, `engine.Update` refuses the download (`ErrFormatChange`) and keeps
the previous cache. The alternative — accepting whatever subset happened to
parse — would silently turn "is a relay egress" into "was in the fraction of
the file we still understood", which is a wrong answer wearing a right
answer's clothes. A stale-but-complete list with a loud warning beats a
fresh-but-partial one.

The empty-body case falls out of the same rule (`0 of 0 rows` parses ⇒
rejected), so an upstream that starts serving empty 200s cannot wipe the
cache either.

## Freshness: TTL floor, StaleAfter, and graceful degradation

Two clocks, deliberately separate (same scheme as tor-exit-lookup):

- **TTL (default 1h, floor 1h)** drives auto-revalidation on `check`. The
  floor matches Apple's own `max-age=3600` — polling faster buys nothing, and
  the conditional GET makes the hourly revalidation nearly free.
- **StaleAfter (7 days)** drives the `status`/`cache_status` warning. Egress
  ranges shift on the order of weeks, not minutes (contrast tor-exit-lookup's
  24h, whose upstream refreshes every 30 minutes), so a week is where "still
  useful" becomes "worth a warning".

`EnsureFresh` degrades rather than fails: when revalidation errors out and a
cached list exists, the caller gets the stale list *and* the error, warns on
stderr, and answers anyway. Only "no cache and the fetch failed" is fatal.
An IR workflow at 3am on a flaky network should get a slightly stale answer
with a warning, not a refusal.

## The usual sibling invariants

Carried over from the tor-exit-lookup / asn-lookup lineage, for the same
reasons as there:

- **One engine, two frontends.** CLI and MCP both drive `engine.Engine`, so
  their answers cannot diverge.
- **Injected clock and fetcher.** `engine.Now` and `apple.Fetcher` are the
  only nondeterminism; tests run hermetic and offline.
- **Atomic store writes** (temp + rename): a crash mid-update never leaves a
  truncated CSV or meta.json to be read back.
- **Grep-style exit codes** for the single-IP text mode (0 relay / 1 not /
  2 error); batch mode reserves the exit code for errors only.
- **usage.md pinned by test**: the MCP manual cannot drift from the
  advertised tools without failing `usage_test.go`.
- **Standard library only.** The tool's job is one HTTP GET, one CSV parse,
  and one hash lookup; a dependency tree would cost more than it pays.

## What this tool deliberately does not know

The list maps egress ranges to intended service areas. A hit therefore means
"this connection exited Apple's relay infrastructure, serving roughly this
area" — it does **not** identify or locate the user (that is Private Relay's
entire purpose), and a miss does not mean "not an Apple user". The tool
reports the signal with its geo hints and leaves interpretation to the
analyst; the MCP usage manual spells this out for agent consumers too.

Reverse enumeration (country → all ranges) was considered and cut: the core
IR question is per-IP, the result sets are huge, and adding them would drag
in the workspace/file-mediation machinery this tool otherwise avoids. See the
RFP's discussion log.
