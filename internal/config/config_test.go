package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// isolateEnv clears every knob this package reads so tests are hermetic.
func isolateEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"ICLOUD_RELAY_LOOKUP_URL", "ICLOUD_RELAY_LOOKUP_STORE_DIR",
		"ICLOUD_RELAY_LOOKUP_TTL_MINUTES", "ICLOUD_RELAY_LOOKUP_AUTO_UPDATE",
		"XDG_CONFIG_HOME", "XDG_DATA_HOME",
	} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

func TestDefaults(t *testing.T) {
	isolateEnv(t)
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.toml"), "", "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.URL != DefaultURL {
		t.Errorf("URL = %q, want default", cfg.URL)
	}
	if cfg.TTL != DefaultTTL {
		t.Errorf("TTL = %v, want %v", cfg.TTL, DefaultTTL)
	}
	if !cfg.AutoUpdate {
		t.Error("AutoUpdate = false, want true")
	}
	if filepath.Base(cfg.CSVPath()) != CSVName || filepath.Base(cfg.MetaPath()) != MetaName {
		t.Errorf("store paths = %q / %q", cfg.CSVPath(), cfg.MetaPath())
	}
}

func TestFileAndPrecedence(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[apple]
url = "https://example.test/list.csv"  # inline comment
ttl_minutes = 240
auto_update = false

[store]
dir = "` + dir + `/store"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, "", "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.URL != "https://example.test/list.csv" {
		t.Errorf("URL = %q", cfg.URL)
	}
	if cfg.TTL != 4*time.Hour {
		t.Errorf("TTL = %v, want 4h", cfg.TTL)
	}
	if cfg.AutoUpdate {
		t.Error("AutoUpdate = true, want false")
	}
	if cfg.StoreDir != dir+"/store" {
		t.Errorf("StoreDir = %q", cfg.StoreDir)
	}

	// Env beats file.
	t.Setenv("ICLOUD_RELAY_LOOKUP_URL", "https://env.test/list.csv")
	cfg, err = Load(path, "", "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.URL != "https://env.test/list.csv" {
		t.Errorf("env override lost: URL = %q", cfg.URL)
	}

	// Flag beats env.
	cfg, err = Load(path, dir+"/flagstore", "https://flag.test/list.csv")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.URL != "https://flag.test/list.csv" || cfg.StoreDir != dir+"/flagstore" {
		t.Errorf("flag override lost: %q %q", cfg.URL, cfg.StoreDir)
	}
}

func TestTTLFloor(t *testing.T) {
	isolateEnv(t)
	t.Setenv("ICLOUD_RELAY_LOOKUP_TTL_MINUTES", "5")
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.toml"), "", "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TTL != MinTTL {
		t.Errorf("TTL = %v, want floored to %v", cfg.TTL, MinTTL)
	}
}

func TestBadTTL(t *testing.T) {
	isolateEnv(t)
	t.Setenv("ICLOUD_RELAY_LOOKUP_TTL_MINUTES", "soon")
	if _, err := Load(filepath.Join(t.TempDir(), "absent.toml"), "", ""); err == nil {
		t.Error("Load accepted a non-numeric TTL")
	}
}

func TestBadTOML(t *testing.T) {
	isolateEnv(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[apple\nurl = x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, "", ""); err == nil {
		t.Error("Load accepted an unterminated section header")
	}
}
