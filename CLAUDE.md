# CLAUDE.md — icloud-relay-lookup

**Organization rules (mandatory): https://github.com/nlink-jp/.github/blob/main/CONVENTIONS.md**

## Purpose

CLI + local MCP server that reports whether an IP address is an **Apple iCloud
Private Relay egress IP**, answered offline from a locally cached copy of
Apple's published egress IP ranges list. A hit also carries the list's geo
hints (country / ISO region / city). The Apple-side sibling of
`tor-exit-lookup` (Tor exits) — together with `asn-lookup` (AS/country) and
`abuse-lookup` (reputation) they profile an IP from four angles.

## Build & test

```bash
make build       # Build → dist/icloud-relay-lookup  (never `go build` directly)
make test        # Tests with race detector + coverage
go test ./...    # Same without Makefile
```

## Architecture

```
main.go                 CLI entry: main.version → app.Run
internal/relaylist/     Parse the egress CSV, per-prefix-length index, longest-match Lookup (pure)
internal/apple/         Fetcher interface + HTTPFetcher (ETag conditional GET, User-Agent)
internal/config/        Sectioned-TOML subset + env/flag resolution (no credentials)
internal/engine/        LoadList / Update (ETag 304-aware, atomic) / Lookup / EnsureFresh / IsStale
internal/app/           Dispatch + check/update/status/mcp; --json, batch, grep-style exit codes
internal/mcp/           Zero-dep stdio JSON-RPC 2.0 server + tools (check_ip/cache_status/update_list/get_usage)
```

Core logic takes io.Reader/io.Writer and injected dependencies for testability
(the fetcher is an interface, mocked in tests). **No external dependencies —
standard library only.** See [docs/en/architecture.md](docs/en/architecture.md)
for the "why".

## Key conventions

- **No credentials:** Apple's egress list endpoint is public. There is no token
  or API key — like tor-exit-lookup, unlike asn-lookup / abuse-lookup.
- **Offline prefix match:** `update` downloads the whole list once (~12MB,
  ~290k prefixes); every `check` is an in-memory longest-prefix match over a
  map-per-prefix-length index (~19 distinct lengths ⇒ ≤19 map probes).
- **Grep-style exit codes** (`check`): `0` = is a Private Relay egress IP,
  `1` = not, `2` = error. Single positional IP + text mode only; batch
  (multiple IPs / stdin / `--json`) exits 0/2.
- **Store = raw CSV + meta.json.** The downloaded CSV is kept verbatim
  (deterministic, re-parsed at load); `meta.json` carries `fetched_at`, the
  `etag`, and counts. Writes are atomic (temp + rename).
- **ETag conditional GET:** `update` sends `If-None-Match`; a 304 just bumps
  `fetched_at`. Auto-revalidate TTL default 1h with a 1h floor
  (`config.MinTTL`) — Apple serves `cache-control: max-age=3600`.
- **Format-change guard:** the CSV format is unofficial and unversioned. If
  fewer than 90% of non-empty rows parse, `Update` fails and the previous
  cache is kept.
- **usage.md is pinned** by `usage_test.go`: adding/renaming a tool or a result
  field means updating the manual, or the test fails.

## Status

Phase 1 in development (`_wip/`, local only — not yet pushed).

## Communication Language

All communication between contributors and Claude Code is conducted in
**Japanese**.
