package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

const DefaultFilename = "eink-server.toml"

type Config struct {
	DeviceListen string `toml:"device_listen"`
	HTTPListen   string `toml:"http_listen"`
	Database     string `toml:"database"`
	LogFormat    string `toml:"log_format"`
}

func Defaults() Config {
	return Config{
		DeviceListen: ":11113",
		HTTPListen:   ":8080",
		Database:     "./data/eink.db",
		LogFormat:    "text",
	}
}

// DefaultPath returns the conventional config location beside the executable.
func DefaultPath() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(executable), DefaultFilename), nil
}

// Load overlays a strict TOML file on Defaults. A missing auto-discovered file
// is allowed; a missing explicitly requested file is an error. Empty and
// whitespace-only files retain all defaults.
func Load(path string, explicit bool) (Config, error) {
	cfg := Defaults()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && !explicit {
		return cfg, nil
	}
	if err != nil {
		return Config{}, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return cfg, nil
	}
	metadata, err := toml.NewDecoder(bytes.NewReader(data)).Decode(&cfg)
	if err != nil {
		return Config{}, fmt.Errorf("decode %s: %w", path, err)
	}
	if unknown := metadata.Undecoded(); len(unknown) != 0 {
		return Config{}, fmt.Errorf("decode %s: unknown setting %q", path, unknown[0])
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate %s: %w", path, err)
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if err := validateListen("device_listen", c.DeviceListen); err != nil {
		return err
	}
	if err := validateListen("http_listen", c.HTTPListen); err != nil {
		return err
	}
	if c.Database == "" {
		return errors.New("database must not be empty")
	}
	if c.LogFormat != "text" && c.LogFormat != "json" {
		return errors.New("log_format must be text or json")
	}
	return nil
}

func validateListen(name, address string) error {
	if address == "" {
		return fmt.Errorf("%s must not be empty", name)
	}
	if _, _, err := net.SplitHostPort(address); err != nil {
		return fmt.Errorf("%s must be a host:port address: %w", name, err)
	}
	return nil
}
