package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBundleConfigWithoutDefaultConfig(t *testing.T) {
	cfw := &ConfigFileWatcher{}
	for _, path := range cfw.GetConfigPaths() {
		if _, err := os.Stat(path); err == nil {
			t.Skipf("default config exists at %s", path)
		}
	}

	bundlePath, configPath, err := cfw.BundleConfig()
	if err != nil {
		t.Fatalf("BundleConfig() returned an error: %v", err)
	}
	if bundlePath != "" || configPath != "" {
		t.Fatalf("BundleConfig() = (%q, %q), want empty paths", bundlePath, configPath)
	}
}

func TestBundleConfigMissingCustomConfigReturnsError(t *testing.T) {
	cfw := &ConfigFileWatcher{customConfigPath: filepath.Join(t.TempDir(), "finicky.js")}

	_, _, err := cfw.BundleConfig()
	if err == nil {
		t.Fatal("BundleConfig() returned nil error for a missing custom config")
	}
}
