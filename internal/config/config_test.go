package config_test

import (
	"path/filepath"
	"testing"

	"github.com/hsleedevelop/bdpeer/internal/config"
)

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{Nickname: "alice", DataDir: dir, EnableBLEWebRTC: true}

	if err := cfg.Save(filepath.Join(dir, "config.json")); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := config.Load(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Nickname != "alice" {
		t.Errorf("got nickname %q, want %q", loaded.Nickname, "alice")
	}
	if loaded.DataDir != dir {
		t.Errorf("got data dir %q, want %q", loaded.DataDir, dir)
	}
	if !loaded.EnableBLEWebRTC {
		t.Error("expected EnableBLEWebRTC to round-trip")
	}
}

func TestDefaultPath(t *testing.T) {
	path := config.DefaultPath()
	if path == "" {
		t.Error("DefaultPath should not be empty")
	}
	if filepath.Base(path) != "config.json" {
		t.Errorf("got %q, want basename config.json", path)
	}
}

func TestLoadMissing(t *testing.T) {
	cfg, err := config.Load("/nonexistent/path/config.json")
	if err != nil {
		t.Fatalf("Load of missing file should return defaults, got: %v", err)
	}
	if cfg.Nickname != "" {
		t.Errorf("expected empty nickname for fresh config")
	}
	if cfg.EnableBLEWebRTC {
		t.Error("expected BLE WebRTC upgrade to default to disabled")
	}
}
