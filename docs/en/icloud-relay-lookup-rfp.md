# RFP: icloud-relay-lookup

> Generated: 2026-07-16
> Status: Draft

## 1. Problem Statement

There is no quick way to determine whether an IP address seen in access logs or alerts is an **Apple iCloud Private Relay egress IP**. Traffic from Private Relay egress nodes carries a distinctive context — "anonymized, but most likely a legitimate user inside the Apple ecosystem" — and should be treated differently from Tor exits or malicious proxies. This tool checks IPs, one-shot or in batch, against the egress IP range list published by Apple, and returns the geo hints included in the list (country / ISO region / city).

Target users are security practitioners doing IR, log investigation, and alert triage (and AI agents doing the same via MCP). It is the fourth member of the IP-context toolset after asn-lookup (attribution), abuse-lookup (reputation), and tor-exit-lookup (Tor exits), and forms the Apple-side counterpart to tor-exit-lookup in the "anonymization network egress detection" category.

## 2. Functional Specification

### Commands / API Surface

CLI (single binary, MCP server included):

| Command | Function |
|---------|----------|
| `icloud-relay-lookup check <ip> [<ip>...]` | Check one or more IPs. With no arguments, reads an IP list from stdin (one per line — pipe in a column cut from logs) |
| `icloud-relay-lookup status` | Show cache state (fetch time, ETag, row count, TTL remaining) |
| `icloud-relay-lookup update` | Force refresh of the list (conditional GET) |
| `icloud-relay-lookup mcp` | Run as an MCP server (stdio) |

MCP tools:

| Tool | Function |
|------|----------|
| `check_ip` | Check an IP (returns result with geo hints) |
| `cache_status` | Cache state |
| `update_list` | Force refresh |
| `get_usage` | Tool reference and error-recovery table |

### Input / Output

- Input: IP addresses (IPv4 / IPv6). CLI takes arguments or stdin (one IP per line)
- Output: both JSON (`--json`) and human-readable

```json
{
  "ip": "172.224.226.34",
  "is_private_relay": true,
  "prefix": "172.224.226.34/31",
  "country": "GB",
  "region": "GB-EN",
  "city": "Oxford",
  "list_fetched_at": "2026-07-16T00:52:21Z"
}
```

- On no match, `is_private_relay: false` with geo fields omitted
- Batch mode emits JSONL, one result per line

### Configuration

- No config file required by default (zero credentials)
- Cache directory: under the OS user cache dir (`os.UserCacheDir()`-based); overridable via environment variable
- TTL floor: 1 hour (matches Apple's `cache-control: max-age=3600`). No network access within TTL; after expiry, revalidate with an ETag conditional GET (304 costs near-zero bandwidth)

### External Dependencies

- Data source: `https://mask-api.icloud.com/egress-ip-ranges.csv` (published by Apple, no authentication)
  - Verified measurements: ~287k rows / 12.1MB; 41,837 IPv4 prefixes + 245,093 IPv6 prefixes (mostly /64)
  - Format: `prefix,country,ISO-region,city,` (fixed 5 fields, trailing empty)
  - Responds with `ETag` + `cache-control: max-age=3600`
- Go external library dependencies: zero (standard library only)

## 3. Design Decisions

- **Language: Go (zero external dependencies)** — same as asn-lookup / tor-exit-lookup. Reuses the `net/netip`-based prefix index from asn-lookup. 287k prefixes are easily handled in memory
- **Single binary with CLI + MCP** — org convention (`mcp` subcommand pattern)
- **New MCP implementation ports the data-toolbox-mcp skeleton** (org standard pattern), though this tool is a stateless checker and needs no workspace model
- **Complements**: completes the IP-context 4-piece set alongside asn-lookup (IP→attribution), abuse-lookup (IP→reputation), and tor-exit-lookup (IP→Tor exit). With all four MCP servers registered, an agent can fetch attribution, reputation, Tor, and Private Relay context for a single IP
- **Explicitly out of scope**:
  - Reverse enumeration (country/region → prefix list) — revisit only when demand appears
  - Detection of general VPNs / proxies / anonymization services other than Private Relay
  - Real-time monitoring / daemon operation

## 4. Development Plan

### Phase 1: Core

- List fetch + local cache with ETag (1-hour TTL floor)
- Tolerant CSV parsing (skip and count malformed rows; detect format changes)
- `net/netip` prefix index construction and lookup logic
- `check` subcommand (single / multiple args / stdin batch; JSON / human output)
- Unit tests (mocked HTTP; real CSV samples as test data)

### Phase 2: Features

- `status` / `update` subcommands
- MCP server (`check_ip` / `cache_status` / `update_list` / `get_usage`)
- E2E tests via the dummy MCP client harness

### Phase 3: Release

- README.md / README.ja.md / CHANGELOG.md / AGENTS.md / LICENSE (MIT)
- `make build-all` (4 platforms); macOS binaries Developer ID signed + notarized
- GitHub release (zips uploaded one by one), homebrew-tap addition
- cybersecurity-series submodule integration, org profile / web catalog dual-surface sync
- `check-org.sh` green

Each phase is independently reviewable.

## 5. Required API Scopes / Permissions

**None** — uses only a public CSV. No API keys, OAuth, or account registration.

## 6. Series Placement

Series: **cybersecurity-series**

Reason: a verdict-style security tool like tor-exit-lookup and abuse-lookup. It belongs next to tor-exit-lookup, its direct sibling in the anonymization-network-egress category.

## 7. External Platform Constraints

- The list is ~12MB. Full download only on first fetch and on format changes; afterwards ETag 304s cost near-zero bandwidth
- `cache-control: max-age=3600` — refreshing more often than hourly is pointless
- **The format is unofficial and unversioned by Apple** — columns may change without notice. Defend with tolerant parsing + validation (if the parse success rate falls below a threshold, keep the previous cache and report an error)
- No published authentication or rate limits; respecting the TTL floor makes this a non-issue in practice
- The list describes egress IP *ranges*; it does not indicate whether an individual IP is currently active (there is no metadata source equivalent to Tor's exit-addresses)

---

## Discussion Log

- **Data source verification (2026-07-16)**: measured `mask-api.icloud.com/egress-ip-ranges.csv` directly: 286,930 rows / 12.1MB; 41,837 IPv4 + 245,093 IPv6 prefixes; 235 countries; 2,140 rows with empty city; all rows fixed 5 fields. Confirmed ETag + max-age=3600, grounding the conditional-GET strategy and the 1-hour TTL floor
- **Tool name**: compared `private-relay-lookup` / `icloud-relay-lookup` / `apple-relay-lookup`; adopted **icloud-relay-lookup**, aligned with Apple's service name
- **Use cases**: two pillars — IR/log investigation and alert triage. Access-control design support was dropped from the primary goals
- **Input modes**: single + stdin batch from v0.1 (batch pays off in log investigation)
- **Reverse enumeration**: decided out of scope (not even Phase 2 — only when demand appears). This removes the need for the workspace-file pattern for large results and keeps the design stateless
- **TTL**: adopted a 1-hour floor matching Apple's max-age=3600, instead of tor-exit-lookup's 30-minute floor
- **Geo hints**: the biggest difference from the Tor list. Results include country/region/city, returning richer context than tor-exit-lookup
