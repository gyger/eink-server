package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/BurntSushi/toml"
)

const DefaultFilename = "eink-server.toml"

type Config struct {
	DeviceListen     string                  `toml:"device_listen"`
	HTTPListen       string                  `toml:"http_listen"`
	Database         string                  `toml:"database"`
	LogFormat        string                  `toml:"log_format"`
	SystemName       string                  `toml:"system_name"`
	DesignDirectory  string                  `toml:"design_directory"`
	DefaultDesign    string                  `toml:"default_design"`
	DefaultRendering string                  `toml:"default_rendering"`
	DefaultTimezone  string                  `toml:"default_timezone"`
	DefaultLocale    string                  `toml:"default_locale"`
	FontDirectory    string                  `toml:"font_directory"`
	UseSystemFonts   bool                    `toml:"use_system_fonts"`
	Actions          map[string]ActionConfig `toml:"actions"`
	Widgets          map[string]WidgetConfig `toml:"widgets"`
}

type WidgetConfig struct {
	Module               string            `toml:"module"`
	AllowedHosts         []string          `toml:"allowed_hosts"`
	Timeout              string            `toml:"timeout"`
	MaxMemoryPages       uint32            `toml:"max_memory_pages"`
	MaxHTTPResponseBytes int64             `toml:"max_http_response_bytes"`
	Config               map[string]string `toml:"config"`
}

type ActionConfig struct {
	Type    string            `toml:"type"`
	URL     string            `toml:"url"`
	Timeout string            `toml:"timeout"`
	Headers map[string]string `toml:"headers"`
}

func Defaults() Config {
	return Config{
		DeviceListen:     ":11113",
		HTTPListen:       ":8080",
		Database:         "./data/eink.db",
		LogFormat:        "text",
		SystemName:       "eink-server",
		DesignDirectory:  "./designs",
		DefaultDesign:    "builtin:status",
		DefaultRendering: "eink",
		DefaultTimezone:  "Europe/Berlin",
		DefaultLocale:    "de-DE",
		FontDirectory:    "./fonts",
		UseSystemFonts:   true,
		Actions:          map[string]ActionConfig{},
		Widgets:          map[string]WidgetConfig{},
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
	if strings.TrimSpace(c.SystemName) == "" {
		return errors.New("system_name must not be empty")
	}
	if c.DefaultDesign != "" && !regexp.MustCompile(`^(builtin|file|db):[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`).MatchString(c.DefaultDesign) {
		return errors.New("default_design must be empty or a builtin:, file:, or db: design ID")
	}
	if c.DefaultRendering != "eink" && c.DefaultRendering != "smooth" {
		return errors.New("default_rendering must be eink or smooth")
	}
	if _, err := time.LoadLocation(c.DefaultTimezone); err != nil {
		return fmt.Errorf("default_timezone is not a valid IANA timezone: %w", err)
	}
	if c.DefaultLocale != "de-DE" && c.DefaultLocale != "en-GB" {
		return errors.New("default_locale must be de-DE or en-GB")
	}
	namePattern := regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	for name, action := range c.Actions {
		if !namePattern.MatchString(name) {
			return fmt.Errorf("invalid action name %q", name)
		}
		if action.Type != "" && action.Type != "webhook" {
			return fmt.Errorf("action %q type must be webhook", name)
		}
		u, err := url.Parse(action.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("action %q requires an HTTP or HTTPS URL", name)
		}
		if action.Timeout != "" {
			d, err := time.ParseDuration(action.Timeout)
			if err != nil || d <= 0 || d > 30*time.Second {
				return fmt.Errorf("action %q timeout must be between 1ns and 30s", name)
			}
		}
		for header, value := range action.Headers {
			if strings.ContainsAny(header+value, "\r\n") {
				return fmt.Errorf("action %q contains an invalid header", name)
			}
		}
	}
	for name, widget := range c.Widgets {
		if !namePattern.MatchString(name) {
			return fmt.Errorf("invalid widget name %q", name)
		}
		if widget.Module == "" {
			return fmt.Errorf("widget %q module must not be empty", name)
		}
		u, err := url.Parse(widget.Module)
		if err != nil || u.IsAbs() || strings.HasPrefix(widget.Module, "//") {
			return fmt.Errorf("widget %q module must be a local path", name)
		}
		if widget.Timeout != "" {
			d, err := time.ParseDuration(widget.Timeout)
			if err != nil || d <= 0 || d > 30*time.Second {
				return fmt.Errorf("widget %q timeout must be between 1ns and 30s", name)
			}
		}
		if widget.MaxMemoryPages > 0 && widget.MaxMemoryPages > 65536 {
			return fmt.Errorf("widget %q max_memory_pages is too large", name)
		}
		if widget.MaxHTTPResponseBytes < 0 || widget.MaxHTTPResponseBytes > 16<<20 {
			return fmt.Errorf("widget %q max_http_response_bytes is invalid", name)
		}
		for _, host := range widget.AllowedHosts {
			if host == "" || strings.ContainsAny(host, "/:*?#@") {
				return fmt.Errorf("widget %q has invalid allowed host %q", name, host)
			}
		}
	}
	return nil
}

func (a ActionConfig) TimeoutDuration() time.Duration {
	if a.Timeout == "" {
		return 5 * time.Second
	}
	d, _ := time.ParseDuration(a.Timeout)
	return d
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
