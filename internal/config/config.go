// Package config handles XDG paths and omavnc settings.
package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type ScaleMode string

const (
	ScaleFit ScaleMode = "fit"
	Scale1x  ScaleMode = "1:1"
	Scale2x  ScaleMode = "2x"
)

func (s ScaleMode) Next() ScaleMode {
	switch s {
	case ScaleFit:
		return Scale1x
	case Scale1x:
		return Scale2x
	default:
		return ScaleFit
	}
}

func (s ScaleMode) String() string {
	if s == "" {
		return string(ScaleFit)
	}
	return string(s)
}

type Config struct {
	DefaultScale ScaleMode `toml:"default_scale"`
	RecentHosts  []string  `toml:"recent_hosts"`
}

func xdg(env, fallback string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, fallback)
}

func Path() string {
	return filepath.Join(xdg("XDG_CONFIG_HOME", ".config"), "omavnc", "config.toml")
}

func StateDir() string { return xdg("XDG_STATE_HOME", ".local/state") }

func Load() Config {
	var c Config
	_, _ = toml.DecodeFile(Path(), &c)
	switch c.DefaultScale {
	case ScaleFit, Scale1x, Scale2x:
	default:
		c.DefaultScale = ScaleFit
	}
	return c
}

func (c Config) Save() error {
	dir := filepath.Dir(Path())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.Create(Path())
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(c)
}

// RememberHost prepends host to the recent list (deduped, max 8) and saves.
func (c *Config) RememberHost(host string) {
	host = strings.TrimSpace(host)
	if host == "" {
		return
	}
	out := []string{host}
	for _, h := range c.RecentHosts {
		if h != host {
			out = append(out, h)
		}
	}
	if len(out) > 8 {
		out = out[:8]
	}
	c.RecentHosts = out
	_ = c.Save()
}
