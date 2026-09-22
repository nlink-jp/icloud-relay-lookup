# AGENTS.md — icloud-relay-lookup

## What this is

A CLI + local MCP server that reports whether an IP address is an **Apple
iCloud Private Relay egress IP**. It answers offline from a locally cached
copy of Apple's published `egress-ip-ranges.csv` (~290k CIDR ranges with geo
hints): `update` downloads the list once, and every `check` is an in-memory
longest-prefix match. The Apple-side sibling of `tor-exit-lookup` (Tor
exits), `asn-lookup` (AS/country), and `abuse-lookup` (reputation).

## Build & test

```bash
make build      # → dist/icloud-relay-lookup  (NEVER `go build` directly)
make test       # go test -race -cover ./...
make check      # lint + test + build-all
make build-all  # cross-compile linux/{amd64,arm64}, darwin/arm64, windows/amd64
make verify-release  # gate: notarized, fresh, runs at this version, clean linux archives (run before upload)
```

`make lint` is `golangci-lint run ./...` with the default linter set.
`.golangci.yml` excludes exactly one thing from errcheck — `fmt.Fprint*`, which
in this repository only ever writes to the stdout/stderr that `app.Run` is
handed — and nothing else. Every other unchecked return is written `_ =` at the
call site with the reason beside it, because an unchecked `Close` on a file this
tool wrote is a real defect class: `Engine.writeFileAtomic` is atomic only because the `Close` error
is checked before the rename. The exclusion matches by function name, not by
destination, so do not write a file with `fmt.Fprint*`. The four sibling lookup
tools carry the same configuration.

Go 1.25+. **No external dependencies** — standard library only.

## Layout

```
main.go                 Entry point; sets main.version, calls app.Run.
internal/relaylist/     CSV parsing + per-prefix-length index + longest-match Lookup + Meta record.
internal/apple/         Fetcher interface + HTTPFetcher (ETag conditional GET, User-Agent).
internal/config/        Sectioned-TOML subset parser + env/flag resolution (no secrets).
internal/engine/        Ties config+fetcher+store: LoadList, Update (304-aware), Lookup, EnsureFresh, IsStale.
internal/app/           CLI: dispatch, check/update/status/mcp, output; grep-style + batch/JSON.
internal/mcp/           Zero-dep stdio JSON-RPC 2.0 server + tools.
  usage.md              Embedded get_usage manual (pinned by usage_test.go).
```

## Key design decisions

- **Offline DB, not an online API.** Like asn-lookup and tor-exit-lookup, the
  whole list is downloaded once and queried locally. `check` only touches the
  network to auto-revalidate a stale cache.
- **No credentials.** The endpoint is public; there is no token or API key to
  configure, log, or leak.
- **Store = raw CSV + meta.json.** The ~12MB CSV is kept verbatim
  (deterministic — re-serializing ~290k rows would only invite drift);
  `meta.json` carries `fetched_at`, the `etag`, and counts. Writes are atomic
  (temp + rename). Freshness lives in `meta.json`, not the file mtime.
- **ETag conditional GET.** `engine.Update` sends `If-None-Match`; a 304 only
  bumps `fetched_at`. Apple serves `cache-control: max-age=3600`, hence the
  1-hour TTL floor (`config.MinTTL`).
- **Per-length prefix index.** One hash map per distinct prefix length
  (~19 in the real list: v4 /24–/32, v6 /40–/64), probed longest-first so a
  more specific range wins. Simple, overlap-safe, and O(#lengths) per lookup.
- **Format-change guard.** The CSV format is unofficial and unversioned. If
  >10% of non-empty rows fail to parse (`engine.maxSkipRatio`), Update returns
  ErrFormatChange and keeps the previous cache.
- **Engine is shared** by CLI and MCP so their behaviour cannot diverge.
- **Fetcher is an interface** (`apple.Fetcher`) so the engine is tested
  without touching the network.
- **Auto-revalidation degrades gracefully.** `engine.EnsureFresh` returns the
  stale cached list alongside the error when a refetch fails, so `check`
  warns and continues offline instead of failing.

## Gotchas

- **Exit-code contract depends on mode:** single positional IP in text mode is
  grep-style tri-state `0` (is relay) / `1` (not) / `2` (error). Multiple IPs,
  stdin, or `--json` switch to batch mode: per-IP results on stdout, exit code
  error-only (`0`/`2`). Don't "normalize" a single-IP not-found to `0`.
- **v4/v6:** addresses and prefixes are canonicalized (`Unmap`, incl.
  v4-mapped *prefixes* like `::ffff:10.0.0.0/120` → `10.0.0.0/24`), so
  `::ffff:1.2.3.4` matches an IPv4 range. The real list is ~15% v4, ~85% v6
  (mostly /64s).
- **Geo hints may be partial:** a few thousand upstream rows have an empty
  city; country/region may still be set. The hint locates the egress range's
  service area, not the user.
- **304 refreshes meta.json only** — the CSV mtime does not change. Anything
  caching the parsed list must key on `meta.json`'s mtime (the MCP server
  does), not the CSV's.
- **usage.md is pinned:** `internal/mcp/usage.md` is embedded and returned by
  `get_usage`. Adding/renaming a tool or a documented result field means
  updating usage.md — `usage_test.go` fails if the manual omits a tool name or
  a key term.
- **MCP has no workspace:** results are small (a yes/no + geo hints), so
  unlike asn-lookup there is no file-mediation.
- **Tool schemas are closed and the decoder enforces it.** Every `inputSchema`
  is built by `obj()` in `internal/mcp/tools.go`, which sets
  `additionalProperties: false` (org ADR-021 §10), and
  `TestEveryToolSchemaIsValidAndClosed` fails if a tool escapes it — so build a
  new schema with `obj()`, not a map literal. The flag is only the *declared*
  half; the enforcing half is `decodeArgs()` beside it
  (`json.Decoder.DisallowUnknownFields`), which every tool handler decodes
  through, including the argument-less ones. **A call carrying an argument this
  server does not declare now fails, naming the field, instead of being
  silently ignored** — deliberate, per ADR-021 §4, because the schema says what
  is allowed and the decoder is what actually refuses. `decodeArgs` also stops
  discarding the decode error, so a wrong-typed argument no longer runs the tool
  on zero values. Omitted or `null` arguments still mean the empty object. Do
  not add a `_ = json.Unmarshal` back, and do not add a compatibility shim: the
  ADR's one-release grace covers only the retired `workspace_root` spellings.
- **Attribution:** keep the Apple data credit in `version` and the READMEs.
  The cached list is local and not redistributed.

## Data source

- `https://mask-api.icloud.com/egress-ip-ranges.csv` — CSV, one range per
  line: `prefix,country,ISO-region,city,` (fixed 5 fields, trailing empty).
  Public, no authentication, served with `ETag` +
  `cache-control: max-age=3600`. ~287k rows / ~12MB as of 2026-07.
