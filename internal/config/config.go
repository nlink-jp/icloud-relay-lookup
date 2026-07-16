// Package config resolves icloud-relay-lookup settings from a sectioned TOML
// file plus environment overrides. It parses only the small TOML subset the
// tool needs, keeping the binary free of external dependencies.
//
// Like tor-exit-lookup (and unlike asn-lookup / abuse-lookup), there are no
// credentials: Apple's egress list endpoint is public, so there is no token
// or API key to configure.
package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultURL is Apple's egress IP ranges endpoint (public, no
	// authentication).
	DefaultURL = "https://mask-api.icloud.com/egress-ip-ranges.csv"

	// DefaultTTL is how old the cached list may be before an auto-revalidation
	// is triggered on the next check (when AutoUpdate is on).
	DefaultTTL = time.Hour
	// MinTTL is the floor on TTL. Apple serves the list with
	// cache-control: max-age=3600, so revalidating more often than hourly is
	// pointless regardless of configuration. Revalidation itself is an ETag
	// conditional GET, so an unchanged list costs almost no bandwidth.
	MinTTL = time.Hour

	// CSVName is the cached list's filename inside the store directory.
	CSVName = "egress-ip-ranges.csv"
	// MetaName is the metadata record's filename inside the store directory.
	MetaName = "meta.json"
)

// Config holds resolved runtime settings.
type Config struct {
	URL        string        // egress-ip-ranges.csv download URL
	StoreDir   string        // directory holding the cached CSV + meta.json
	TTL        time.Duration // auto-revalidation threshold (floored at MinTTL)
	AutoUpdate bool          // auto-revalidate on check when older than TTL
}

// CSVPath returns the cached list's location.
func (c *Config) CSVPath() string { return filepath.Join(c.StoreDir, CSVName) }

// MetaPath returns the metadata record's location.
func (c *Config) MetaPath() string { return filepath.Join(c.StoreDir, MetaName) }

// Load resolves configuration. If configPath is empty the default location
// (~/.config/icloud-relay-lookup/config.toml) is used when present.
// Environment variables override file values, and any explicit non-empty
// override* argument wins over both.
func Load(configPath, storeDirOverride, urlOverride string) (*Config, error) {
	cfg := &Config{
		URL:        DefaultURL,
		StoreDir:   DefaultStoreDir(),
		TTL:        DefaultTTL,
		AutoUpdate: true,
	}

	if configPath == "" {
		configPath = DefaultConfigPath()
	}
	if configPath != "" {
		if f, err := os.Open(configPath); err == nil {
			defer f.Close()
			sections, perr := parseTOML(f)
			if perr != nil {
				return nil, fmt.Errorf("parse config %s: %w", configPath, perr)
			}
			if aerr := applySections(cfg, sections); aerr != nil {
				return nil, fmt.Errorf("config %s: %w", configPath, aerr)
			}
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("open config %s: %w", configPath, err)
		}
	}

	// Environment overrides.
	if v := os.Getenv("ICLOUD_RELAY_LOOKUP_URL"); v != "" {
		cfg.URL = v
	}
	if v := os.Getenv("ICLOUD_RELAY_LOOKUP_STORE_DIR"); v != "" {
		cfg.StoreDir = v
	}
	if v := os.Getenv("ICLOUD_RELAY_LOOKUP_TTL_MINUTES"); v != "" {
		d, err := parseTTLMinutes(v)
		if err != nil {
			return nil, fmt.Errorf("ICLOUD_RELAY_LOOKUP_TTL_MINUTES: %w", err)
		}
		cfg.TTL = d
	}
	if v := os.Getenv("ICLOUD_RELAY_LOOKUP_AUTO_UPDATE"); v != "" {
		if b, ok := parseBool(v); ok {
			cfg.AutoUpdate = b
		}
	}

	// Explicit flag overrides win.
	if urlOverride != "" {
		cfg.URL = urlOverride
	}
	if storeDirOverride != "" {
		cfg.StoreDir = storeDirOverride
	}

	// Enforce the revalidation floor on TTL.
	if cfg.TTL < MinTTL {
		cfg.TTL = MinTTL
	}

	return cfg, nil
}

func applySections(cfg *Config, sections map[string]map[string]string) error {
	if a := sections["apple"]; a != nil {
		if v := a["url"]; v != "" {
			cfg.URL = v
		}
		if v := a["ttl_minutes"]; v != "" {
			d, err := parseTTLMinutes(v)
			if err != nil {
				return fmt.Errorf("[apple] ttl_minutes: %w", err)
			}
			cfg.TTL = d
		}
		if v := a["auto_update"]; v != "" {
			if b, ok := parseBool(v); ok {
				cfg.AutoUpdate = b
			}
		}
	}
	if s := sections["store"]; s != nil {
		if v := s["dir"]; v != "" {
			cfg.StoreDir = expandHome(v)
		}
	}
	return nil
}

// parseTTLMinutes parses a non-negative minutes value into a Duration.
func parseTTLMinutes(v string) (time.Duration, error) {
	m, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", v)
	}
	if m < 0 {
		return 0, fmt.Errorf("must not be negative")
	}
	return time.Duration(m * float64(time.Minute)), nil
}

// parseBool accepts the common truthy/falsey spellings.
func parseBool(v string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	}
	return false, false
}

// DefaultConfigPath returns the default config file location, honoring
// XDG_CONFIG_HOME.
func DefaultConfigPath() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "icloud-relay-lookup", "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "icloud-relay-lookup", "config.toml")
}

// DefaultStoreDir returns the default store directory, honoring XDG_DATA_HOME.
func DefaultStoreDir() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "icloud-relay-lookup")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".local", "share", "icloud-relay-lookup")
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// parseTOML parses the minimal subset icloud-relay-lookup needs: [section]
// headers and key = value lines, where value is an optionally quoted string.
// Comments start with '#'. It intentionally does not support arrays, nested
// tables, or typed values.
func parseTOML(r io.Reader) (map[string]map[string]string, error) {
	sections := map[string]map[string]string{}
	current := "" // top-level keys land in the "" section
	sections[current] = map[string]string{}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		if strings.HasPrefix(raw, "[") {
			end := strings.IndexByte(raw, ']')
			if end < 0 {
				return nil, fmt.Errorf("line %d: unterminated section header", line)
			}
			current = strings.TrimSpace(raw[1:end])
			if _, ok := sections[current]; !ok {
				sections[current] = map[string]string{}
			}
			continue
		}
		eq := strings.IndexByte(raw, '=')
		if eq < 0 {
			return nil, fmt.Errorf("line %d: expected key = value", line)
		}
		key := strings.TrimSpace(raw[:eq])
		val := parseValue(strings.TrimSpace(raw[eq+1:]))
		if key == "" {
			return nil, fmt.Errorf("line %d: empty key", line)
		}
		sections[current][key] = val
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return sections, nil
}

// parseValue strips surrounding quotes, or trims a trailing inline comment
// from a bare value.
func parseValue(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') {
		q := v[0]
		if end := strings.IndexByte(v[1:], q); end >= 0 {
			return v[1 : 1+end]
		}
	}
	if hash := strings.IndexByte(v, '#'); hash >= 0 {
		v = strings.TrimSpace(v[:hash])
	}
	return v
}
