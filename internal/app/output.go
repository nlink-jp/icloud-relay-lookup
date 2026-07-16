package app

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/nlink-jp/icloud-relay-lookup/internal/engine"
	"github.com/nlink-jp/icloud-relay-lookup/internal/relaylist"
)

// checkJSON is the JSONL shape for `check --json` results (one object per line).
type checkJSON struct {
	IP             string    `json:"ip"`
	IsPrivateRelay bool      `json:"is_private_relay"`
	Error          string    `json:"error,omitempty"`
	Prefix         string    `json:"prefix,omitempty"`
	Country        string    `json:"country,omitempty"`
	Region         string    `json:"region,omitempty"`
	City           string    `json:"city,omitempty"`
	CheckedAt      time.Time `json:"checked_at"`
	ListFetchedAt  time.Time `json:"list_fetched_at"`
}

// emitResult writes one lookup result as text or a JSON line.
func emitResult(out io.Writer, ip string, r engine.Result, jsonOut bool, list *relaylist.List, now time.Time) {
	if jsonOut {
		j := checkJSON{IP: ip, IsPrivateRelay: r.IsRelay, CheckedAt: now, ListFetchedAt: list.Fetched()}
		if r.IsRelay {
			j.Prefix = r.Entry.Prefix.String()
			j.Country = r.Entry.Country
			j.Region = r.Entry.Region
			j.City = r.Entry.City
		}
		_ = jsonLine(out, j)
		return
	}
	if !r.IsRelay {
		fmt.Fprintf(out, "%s is not an iCloud Private Relay egress IP\n", ip)
		return
	}
	line := ip + " is an iCloud Private Relay egress IP"
	if hint := geoHint(r.Entry); hint != "" {
		line += "  [" + hint + "]"
	}
	fmt.Fprintln(out, line)
}

// geoHint renders the entry's location fields plus the matched range, most
// specific first, skipping whatever is empty.
func geoHint(e relaylist.Entry) string {
	var parts []string
	for _, p := range []string{e.City, e.Region, e.Country} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	loc := strings.Join(parts, ", ")
	if loc == "" {
		return e.Prefix.String()
	}
	return loc + " — " + e.Prefix.String()
}

// emitInvalid writes an invalid-address entry (batch/JSON mode only; the
// single-IP tri-state path reports invalid input on stderr instead).
func emitInvalid(out io.Writer, ip string, jsonOut bool, list *relaylist.List, now time.Time) {
	if jsonOut {
		_ = jsonLine(out, checkJSON{IP: ip, Error: "invalid address", CheckedAt: now, ListFetchedAt: list.Fetched()})
		return
	}
	fmt.Fprintf(out, "%s: invalid address\n", ip)
}

// jsonLine writes v as a single JSON line.
func jsonLine(w io.Writer, v any) error {
	return json.NewEncoder(w).Encode(v)
}
