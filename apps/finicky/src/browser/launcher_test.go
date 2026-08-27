package browser

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseProfilesAcceptsProfileNameAndDirectory(t *testing.T) {
	localStatePath := filepath.Join(t.TempDir(), "Local State")
	if err := os.WriteFile(localStatePath, []byte(`{"profile":{"info_cache":{"Profile 1":{"name":"Work Profile"}}}}`), 0600); err != nil {
		t.Fatal(err)
	}

	for _, profile := range []string{"Work Profile", "Profile 1"} {
		got, ok := parseProfiles(localStatePath, profile)
		if !ok || got != "Profile 1" {
			t.Fatalf("parseProfiles(%q) = %q, %v; want Profile 1, true", profile, got, ok)
		}
	}
}

func TestBuildProfileLaunchArgsPreservesProfileDirectoryAndURL(t *testing.T) {
	config := BrowserConfig{
		Name:    "Google Chrome",
		AppType: "appName",
		Profile: "Work Profile",
		URL:     "https://example.invalid/",
	}

	got := buildProfileLaunchArgs(config, []string{"--profile-directory=Profile 1"})
	want := []string{"--profile-directory=Profile 1", "https://example.invalid/"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildProfileLaunchArgs() = %#v, want %#v", got, want)
	}
}

func TestAppBundleExecutablePathUsesBundleMetadata(t *testing.T) {
	bundlePath := filepath.Join(t.TempDir(), "Firefox.app")
	infoPath := filepath.Join(bundlePath, "Contents", "Info.plist")
	if err := os.MkdirAll(filepath.Dir(infoPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(infoPath, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict><key>CFBundleExecutable</key><string>firefox-bin</string></dict></plist>`), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := appBundleExecutablePath(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(bundlePath, "Contents", "MacOS", "firefox-bin")
	if got != want {
		t.Fatalf("appBundleExecutablePath() = %q, want %q", got, want)
	}
}

func TestLaunchProfileBrowserUsesProfileArguments(t *testing.T) {
	bundlePath := filepath.Join(t.TempDir(), "Google Chrome.app")
	infoPath := filepath.Join(bundlePath, "Contents", "Info.plist")
	executablePath := filepath.Join(bundlePath, "Contents", "MacOS", "Google Chrome")
	if err := os.MkdirAll(filepath.Dir(executablePath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(infoPath, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict><key>CFBundleExecutable</key><string>Google Chrome</string></dict></plist>`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executablePath, nil, 0755); err != nil {
		t.Fatal(err)
	}

	if err := launchProfileBrowser(BrowserConfig{Name: bundlePath}, true, []string{"--profile-directory=Profile 1"}, nil); err != nil {
		t.Fatalf("launchProfileBrowser() error = %v, want nil", err)
	}
}

func TestLaunchProfileBrowserRequestsSourceAppRestore(t *testing.T) {
	bundlePath := filepath.Join(t.TempDir(), "Google Chrome.app")
	infoPath := filepath.Join(bundlePath, "Contents", "Info.plist")
	executablePath := filepath.Join(bundlePath, "Contents", "MacOS", "Google Chrome")
	if err := os.MkdirAll(filepath.Dir(executablePath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(infoPath, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict><key>CFBundleExecutable</key><string>Google Chrome</string><key>CFBundleIdentifier</key><string>com.google.Chrome</string></dict></plist>`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executablePath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}

	restored := make(chan string, 1)
	if err := launchProfileBrowser(BrowserConfig{Name: bundlePath}, false, []string{"--profile-directory=Profile 1"}, func(bundleID string) {
		restored <- bundleID
	}); err != nil {
		t.Fatalf("launchProfileBrowser() error = %v, want nil", err)
	}

	select {
	case bundleID := <-restored:
		if bundleID != "com.google.Chrome" {
			t.Fatalf("restore bundle ID = %q, want com.google.Chrome", bundleID)
		}
	default:
		t.Fatal("profile launch did not request restoring the source application")
	}
}

func TestShouldOpenInBackgroundUsesBrowserSetting(t *testing.T) {
	openInBackground := false
	if shouldOpenInBackground(BrowserConfig{OpenInBackground: &openInBackground}, true) {
		t.Fatal("browser setting did not override the default")
	}
}

func TestBrowserInfoMatchesApplicationPathWithSpaces(t *testing.T) {
	browser := browserInfo{ID: "com.google.Chrome", AppName: "Google Chrome"}
	if !matchesBrowser(browser, "/Applications/Google Chrome.app") {
		t.Fatal("application path did not match Google Chrome")
	}
}

func TestLaunchBrowserRejectsUnresolvedRequestedProfile(t *testing.T) {
	config := BrowserConfig{
		Name:    "Unknown Browser",
		AppType: "appName",
		Profile: "Work Profile",
		URL:     "https://example.invalid/",
	}

	if err := LaunchBrowser(config, true, false, nil); err == nil {
		t.Fatal("LaunchBrowser() succeeded without the requested profile")
	}
}
