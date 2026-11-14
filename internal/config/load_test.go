package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFreshInstallHasNoConnections(t *testing.T) {
	cfg, saved, err := Load(filepath.Join(t.TempDir(), "missing", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if saved {
		t.Error("a missing config file must not count as saved")
	}
	if len(cfg.Connections) != 0 {
		t.Errorf("a fresh install must not connect to anything on its own (this machine may have Docker or kubectl), got %+v", cfg.Connections)
	}
	if cfg.WindowMinutes != 30 {
		t.Errorf("window = %d, want the 30 minute default", cfg.WindowMinutes)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("the empty default config must be valid: %v", err)
	}
}

func TestLoadReadsASavedConfigUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	want := Config{WindowMinutes: 45, Connections: []Connection{{ID: "a", Type: TypeDocker, Name: "mine", Enabled: true}}}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, saved, err := Load(path)
	if err != nil || !saved || len(got.Connections) != 1 || got.Connections[0].Name != "mine" || got.WindowMinutes != 45 {
		t.Errorf("got=%+v saved=%v err=%v", got, saved, err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("config file mode = %v, want 0600", st.Mode().Perm())
	}
}

func TestAnExplicitlyEmptySavedConfigStaysEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := Save(path, Config{WindowMinutes: 30}); err != nil {
		t.Fatal(err)
	}
	got, saved, err := Load(path)
	if err != nil || !saved || len(got.Connections) != 0 {
		t.Errorf("a user who removed every connection must not get any back: %+v saved=%v err=%v", got, saved, err)
	}
}
