# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Fixed

- **`make verify-release` now fails closed.** Its last block chained unzip, the
  packaged binary's `--version` and `spctl` with `&&` and ended the whole chain
  in `|| true`, so a zip that did not unpack or a binary that did not run exited
  0 and the upload proceeded. Each step is now judged on its own, the packaged
  binary's `--version` must contain the tag being released, and only the
  informational `spctl` line may be ignored. Matches the org template
  (CONVENTIONS.md §Code Signing → Verifying a release).

## [0.2.0] - 2026-09-21

### Changed

- **An MCP tool call carrying an argument the tool does not declare now fails
  instead of being quietly ignored.** This is a deliberate behaviour change,
  required by org ADR-021 §4. Until now a misspelt argument was dropped and the
  call ran without it: a batch of addresses sent under a misspelt `ips`
  checked nothing at all and came back with "provide 'ip'", which reads as a
  missing argument rather than a mistyped one. Every tool — including
  `get_usage`, `update_list` and `cache_status`, which take no arguments — now
  decodes with `DisallowUnknownFields` and refuses the call, naming the
  offending field: `arguments: json: unknown field "ipx"`.

  A malformed argument object is refused for the same reason. The decode error
  used to be discarded along with the unknown field, so `{"ip": 8}` ran as if
  no address had been supplied. It now reports the type mismatch.

  Nothing runs before the arguments decode, so a rejected call reads no list
  and downloads nothing. Omitting `arguments`, or sending `{}` or `null`, still
  means "no arguments" and is not an error. There is no compatibility shim: an
  argument name this server does not declare has never meant anything, so the
  only fix is to correct it.

### Fixed

- **Every MCP tool input schema is closed.** The schemas omitted
  `additionalProperties: false`, so a mistyped argument read as a legitimate one
  to any client that validates against them. Schemas are now built through a
  single `obj()` helper that sets the flag, and an arch test fails if a tool's
  schema omits it — org ADR-021 §10 requires the test as well as the flag,
  because a rule stated only in prose is re-decided by whoever adds the next
  tool.

## [0.1.1] - 2026-09-21

### Fixed

- A number in the config file was accepted when it was not one. `NaN` passed the
  range check — it fails every comparison, so "reject what is below the floor"
  lets it through — and `Inf` or `1e300` overflowed the duration it became.
  Ranges are now stated from the inside, with a ceiling.

- `make check` is green again: `make lint` failed on errcheck findings for every
  `fmt.Fprint*` write to the CLI's own stdout/stderr, and on deliberate discards
  that did not say they were deliberate. No behaviour change.
  - `.golangci.yml` excludes only `fmt.Fprint*` from errcheck, so errcheck stays
    meaningful everywhere else (the atomic write checks its `Close`).
  - Every other unchecked return is now `_ =` with the reason beside it.

### Added

- `make verify-release` refuses to publish a darwin zip that carries no
  notarization marker, or one rebuilt after its marker. The vendored Homebrew
  templates and packaging scripts are in sync with the org canonical.

## [0.1.0] - 2026-07-16

### Added

- Initial release.
- `check <IP>...` — report whether IPs are Apple iCloud Private Relay egress
  IPs, answered offline from the cached list. A single positional IP in text
  mode uses grep-style exit codes (`0` = relay, `1` = not, `2` = error);
  multiple IPs, stdin, or `--json` switch to batch mode (per-IP results on
  stdout, error-only exit code). `--json` emits JSON Lines. On a hit, the
  matched prefix and Apple's geo hints (country / ISO region / city) are shown.
- `update` — revalidate/download `egress-ip-ranges.csv` and rebuild the local
  store (atomic temp + rename; the CSV is kept verbatim beside a `meta.json`
  carrying fetch time, ETag, and counts). Revalidation is an ETag conditional
  GET: a 304 only bumps freshness. A download that no longer parses (>10% of
  rows unparseable — the format is unofficial and unversioned) is rejected and
  the previous cache is kept.
- `status` — show the cached list's fetch time, range counts (v4/v6), ETag,
  and staleness (`StaleAfter` = 7 days).
- `mcp` — local stdio MCP server (JSON-RPC 2.0, standard library only)
  exposing `check_ip`, `cache_status`, `update_list`, and `get_usage`.
  `get_usage` returns an embedded operating manual, advertised via the
  initialize `instructions` field.
- Auto-revalidation: `check` revalidates when the cached list is older than
  the TTL (default 1h, floored at 1h to match Apple's
  `cache-control: max-age=3600`). A failure falls back to the cached list with
  a warning. Disable with `--no-update` or `[apple] auto_update = false`.
- Offline lookup index: the CSV parses into a hash map per distinct prefix
  length (~19 in the real list), matched longest-first; addresses are
  canonicalized (`Unmap`) so v4-in-v6 inputs match. Freshness lives in
  `meta.json`, not the file mtime.
- Configuration via sectioned TOML (`~/.config/icloud-relay-lookup/config.toml`)
  and `ICLOUD_RELAY_LOOKUP_*` environment variables (`URL`, `STORE_DIR`,
  `TTL_MINUTES`, `AUTO_UPDATE`). No credentials required.
- Fetch etiquette: a descriptive `User-Agent` on every request; ETag
  conditional GETs and the 1-hour TTL floor keep upstream load negligible.
- Zero external dependencies (standard library only).
- Apple data-source attribution in `version` and the READMEs.
