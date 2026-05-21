package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// TURNServer holds credentials for a TURN relay server.
type TURNServer struct {
	URL        string `json:"url"`
	Username   string `json:"username"`
	Credential string `json:"credential"`
}

// DefaultTURNServers are free public TURN relays used when no custom servers are configured.
// Traffic is DTLS-encrypted end-to-end; the relay sees packet sizes/IPs but not content.
var DefaultTURNServers = []TURNServer{
	{URL: "turn:openrelay.metered.ca:80", Username: "openrelayproject", Credential: "openrelayproject"},
	{URL: "turn:openrelay.metered.ca:443", Username: "openrelayproject", Credential: "openrelayproject"},
	{URL: "turns:openrelay.metered.ca:443", Username: "openrelayproject", Credential: "openrelayproject"},
}

type Config struct {
	Nickname      string `json:"nickname"`
	DataDir       string `json:"data_dir"`
	PrivateKeyB64 string `json:"private_key_b64,omitempty"`
	// EnableBLEWebRTC allows macOS BLE discovery to upgrade the data path to WebRTC.
	// Keep false when BLE must be the only cross-network channel.
	EnableBLEWebRTC bool `json:"enable_ble_webrtc,omitempty"`
	// TURNServers overrides the built-in Open Relay servers when non-empty.
	// Set to your own coturn instance for full privacy.
	TURNServers []TURNServer `json:"turn_servers,omitempty"`
}

// ICEServers returns the TURN servers to use: custom if configured, otherwise defaults.
func (c *Config) ICEServers() []TURNServer {
	if len(c.TURNServers) > 0 {
		return c.TURNServers
	}
	return DefaultTURNServers
}

func DefaultPath() string {
	switch runtime.GOOS {
	case "windows":
		base := os.Getenv("APPDATA")
		if base == "" {
			base, _ = os.UserHomeDir()
		}
		return filepath.Join(base, "bdpeer", "config.json")
	case "darwin":
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Library", "Application Support", "bdpeer", "config.json")
	default:
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			return filepath.Join(xdg, "bdpeer", "config.json")
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".config", "bdpeer", "config.json")
	}
}

func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var cfg Config
	if err := json.NewDecoder(f).Decode(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".bdpeer-cfg-*")
	if err != nil {
		return err
	}
	if err := json.NewEncoder(f).Encode(c); err != nil {
		f.Close()
		os.Remove(f.Name())
		return err
	}
	f.Close()
	return os.Rename(f.Name(), path)
}
