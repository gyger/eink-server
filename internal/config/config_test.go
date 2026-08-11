package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMissingAutomaticConfigUsesDefaults(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "missing.toml"), false)
	if err != nil {
		t.Fatal(err)
	}
	if got != Defaults() {
		t.Fatalf("Load() = %+v, want %+v", got, Defaults())
	}
}

func TestEmptyConfigUsesDefaults(t *testing.T) {
	path := writeConfig(t, " \n\t")
	got, err := Load(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if got != Defaults() {
		t.Fatalf("Load() = %+v, want %+v", got, Defaults())
	}
}

func TestConfigOverlaysDefaults(t *testing.T) {
	path := writeConfig(t, "http_listen = \"127.0.0.1:9090\"\nlog_format = \"json\"\n")
	got, err := Load(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.HTTPListen != "127.0.0.1:9090" || got.LogFormat != "json" || got.DeviceListen != Defaults().DeviceListen {
		t.Fatalf("unexpected config: %+v", got)
	}
}

func TestExplicitMissingConfigFails(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "missing.toml"), true); err == nil {
		t.Fatal("explicit missing config accepted")
	}
}

func TestUnknownAndInvalidValuesFail(t *testing.T) {
	for _, contents := range []string{
		"unknown = true",
		"http_listen = \"8080\"",
		"log_format = \"pretty\"",
		"http_listen =",
	} {
		if _, err := Load(writeConfig(t, contents), true); err == nil {
			t.Fatalf("invalid config accepted: %s", contents)
		}
	}
}

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
