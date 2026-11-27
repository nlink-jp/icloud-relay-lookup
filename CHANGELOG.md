# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- **The MCP server speaks both eras of the protocol** (ADR-0001): the
  2026-07-28 revision, whose clients probe with `server/discover`, as well as
  the earlier `initialize` handshake (2025-11-25, 2025-06-18, 2024-11-05). The
  protocol layer is now the organization's shared module nlink-jp/mcp
  (organization ADR-030); the tools, their arguments and their results are
  unchanged.

### Changed

- Protocol details now follow the module:
  - `tools/list`, `tools/call` and unknown methods before `initialize` are
    refused with -32602 (an unknown method was -32601);
  - `arguments` that is not an object is refused with -32602 before reaching
    the tool;
  - a request with `"id":null`, or without `"jsonrpc":"2.0"`, gets -32600;
  - messages are read one per line (up to 16 MiB) instead of as a JSON stream;
  - a parse error or a batch no longer ends the server (a batch gets -32600);
  - a panic in a tool is answered -32603 and the server goes on (it crashed);
  - up to 256 calls wait behind a running one; more are refused with -32603
    "server busy";
  - `notifications/cancelled` now cancels the call A cancelled `update_list` stops its download; the list is written to a temporary file and renamed, so the stored list stays as it was (a test pins it).

  Requests piped in are still all answered before the server exits at end of
  input.

### Fixed

- An argument spelt in another case (`{"IP": ...}`) was accepted as the declared one; it is now refused by name.
- SIGINT or SIGTERM ended the MCP server by killing the process; a signal now ends it through its context, so its cleanup runs.

## [0.2.2] - 2026-11-27

### Changed

- `make vuln` — and so `make package` — scans every platform in `PLATFORMS`,
  with the environment `build-all` builds each with. It scanned the host's
  build alone, so an advisory in a Linux- or Windows-only package could not
  stop a release.

### Fixed

- **`initialize` no longer echoes back whichever protocol version the client
  asks for.** The server answers with the requested revision only when it
  satisfies it (2025-11-25, 2025-06-18 or 2024-11-05), and with 2025-11-25
  otherwise, as the MCP lifecycle requires. A client asking for 2026-07-28 was
  told the server spoke it, while the server went on speaking the
  handshake-based protocol. 2025-03-26 is not offered: it requires accepting
  JSON-RPC batches, which this server does not.

## [0.2.1] - 2026-11-14

### Security

- **Rebuilt with Go 1.27.4, which `go.mod` now requires** (it was `go 1.25.0`).
  No code of the tool changed. v0.2.0 was built with Go 1.27.1; `govulncheck
  -mode binary` finds 10 standard-library advisories in its darwin-arm64
  binary, in `crypto/tls`, `crypto/x509`, `net/http` and `net/textproto`:
  GO-2026-6603, GO-2026-6605, GO-2026-6607, GO-2026-6608, GO-2026-6610,
  GO-2026-6611, GO-2026-6613, GO-2026-6617, GO-2026-6809, GO-2026-6859. All are
  fixed by Go 1.27.4. Binary mode shows that an affected function is linked
  into the binary, not that the tool calls it; each advisory is described at
  `https://pkg.go.dev/vuln/<ID>`.

### Changed

- `make package` runs `govulncheck ./...` first (`make vuln`) and stops on a
  finding, so a release cannot be built past one.

### Fixed

- **`make verify-release` now fails closed.** Its last block chained unzip, the
  packaged binary's `--version` and `spctl` with `&&` and ended the whole chain
  in `|| true`, so a zip that did not unpack or a binary that did not run exited
  0 and the upload proceeded. Each step is now judged on its own, the packaged
  binary's `--version` must contain the tag being released, and only the
  informational `spctl` line may be ignored. Matches the org template
  (CONVENTIONS.md §Code Signing → Verifying a release).
- **The Linux archives no longer carry macOS file metadata.** macOS `tar` wrote
  each bundled file's extended attributes (`com.apple.provenance`, and a Dropbox
  attribute where the tree is synced) into the `.tar.gz` twice: as AppleDouble
  `._` members, which GNU tar extracts as stray `._<name>` files beside the real
  ones, and as `LIBARCHIVE.xattr.*` / `SCHILY.xattr.*` pax headers, which it
  reports as unknown keywords. `make package` now archives with
  `COPYFILE_DISABLE=1 tar --no-xattrs`; each setting stops one of the two.
  Archives already published still carry them; the files themselves are
  unaffected.

### Internal

- `make verify-release` also judges each Linux archive: no AppleDouble or other
  macOS metadata members — listed with `--options 'tar:!mac-ext'`, because a
  plain macOS listing folds `._` members away — no extended attributes as pax
  headers, and exactly the canonical binary, `README.md` and `LICENSE`, compared
  in the C locale.
- The Linux-archive check in `make verify-release` reads each archive's pax
  headers with Python's `tarfile` instead of grepping the decompressed stream,
  which also matched file text that names the keywords (a bundled CHANGELOG,
  for one).

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
