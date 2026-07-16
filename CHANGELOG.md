# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- Initial implementation.
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
