package browser

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"al.essio.dev/pkg/shellescape"
	"finicky/util"
)

//go:embed browsers.json
var browsersJsonData []byte

type BrowserResult struct {
	Browser BrowserConfig `json:"browser"`
	Error   string        `json:"error"`
}

type BrowserConfig struct {
	Name             string   `json:"name"`
	AppType          string   `json:"appType"`
	OpenInBackground *bool    `json:"openInBackground"`
	Profile          string   `json:"profile"`
	Args             []string `json:"args"`
	URL              string   `json:"url"`
}

type browserInfo struct {
	ConfigDirRelative string `json:"config_dir_relative"`
	ID                string `json:"id"`
	AppName           string `json:"app_name"`
	Type              string `json:"type"`
}

func LaunchBrowser(config BrowserConfig, dryRun bool, openInBackgroundByDefault bool, restoreSourceApp func(string)) error {
	if config.AppType == "none" {
		slog.Info("AppType is 'none', not launching any browser")
		return nil
	}

	slog.Info("Starting browser", "name", config.Name, "url", config.URL)

	profileArgs, profileFound := resolveBrowserProfileArgs(config.Name, config.Profile)
	if config.Profile != "" && !profileFound {
		return fmt.Errorf("requested browser profile could not be resolved")
	}
	openInBackground := shouldOpenInBackground(config, openInBackgroundByDefault)
	if profileFound {
		if !openInBackground {
			restoreSourceApp = nil
		}
		return launchProfileBrowser(config, dryRun, profileArgs, restoreSourceApp)
	}

	openArgs := buildOpenArgs(config, openInBackground)
	cmd := exec.Command("open", openArgs...)
	return runOpenCommand(cmd, dryRun)
}

func launchProfileBrowser(config BrowserConfig, dryRun bool, profileArgs []string, restoreSourceApp func(string)) error {
	bundlePath, err := profileBrowserBundlePath(config)
	if err != nil {
		return err
	}
	bundleID := ""
	if restoreSourceApp != nil {
		bundleID, err = appBundleIdentifier(bundlePath)
		if err != nil {
			return err
		}
	}
	executable, err := profileBrowserExecutable(bundlePath)
	if err != nil {
		return err
	}

	cmd := exec.Command(executable, buildProfileLaunchArgs(config, profileArgs)...)
	prettyCmd := formatCommand(cmd.Path, cmd.Args)
	if dryRun {
		slog.Debug("Would run command (dry run)", "command", prettyCmd)
		return nil
	}

	slog.Debug("Run command", "command", prettyCmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	if restoreSourceApp != nil {
		restoreSourceApp(bundleID)
	}

	go func() {
		if err := cmd.Wait(); err != nil {
			slog.Error("Browser process exited with error", "error", err)
		}
	}()

	return nil
}

func profileBrowserExecutable(bundlePath string) (string, error) {
	executable, err := appBundleExecutablePath(bundlePath)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(executable)
	if err != nil {
		return "", fmt.Errorf("could not find browser executable for profile launch: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("browser executable for profile launch is a directory")
	}
	return executable, nil
}

func profileBrowserBundlePath(config BrowserConfig) (string, error) {
	if filepath.Ext(config.Name) == ".app" {
		return config.Name, nil
	}

	candidates := []string{filepath.Join("/Applications", config.Name+".app")}
	if homeDir, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(homeDir, "Applications", config.Name+".app"))
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	query := fmt.Sprintf("kMDItemDisplayName == %q", config.Name)
	if config.AppType == "bundleId" {
		query = fmt.Sprintf("kMDItemCFBundleIdentifier == %q", config.Name)
	}
	output, err := exec.Command("mdfind", query).Output()
	if err != nil {
		return "", fmt.Errorf("could not find browser application for profile launch: %w", err)
	}
	for _, candidate := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if filepath.Ext(candidate) == ".app" {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not find browser application for profile launch")
}

func appBundleExecutablePath(bundlePath string) (string, error) {
	name, err := appBundleInfoValue(bundlePath, "CFBundleExecutable")
	if err != nil {
		return "", err
	}
	if name == "" || filepath.Base(name) != name {
		return "", fmt.Errorf("browser executable metadata is invalid")
	}
	return filepath.Join(bundlePath, "Contents", "MacOS", name), nil
}

func appBundleIdentifier(bundlePath string) (string, error) {
	return appBundleInfoValue(bundlePath, "CFBundleIdentifier")
}

func appBundleInfoValue(bundlePath string, key string) (string, error) {
	infoPath := filepath.Join(bundlePath, "Contents", "Info.plist")
	output, err := exec.Command("plutil", "-extract", key, "raw", "-o", "-", infoPath).Output()
	if err != nil {
		return "", fmt.Errorf("could not read browser bundle metadata: %w", err)
	}
	value := strings.TrimSpace(string(output))
	if value == "" {
		return "", fmt.Errorf("browser bundle metadata is empty")
	}
	return value, nil
}

func buildProfileLaunchArgs(config BrowserConfig, profileArgs []string) []string {
	args := append([]string{}, profileArgs...)
	for _, arg := range config.Args {
		if arg != "--args" {
			args = append(args, arg)
		}
	}
	if len(config.Args) == 0 {
		args = append(args, config.URL)
	}
	return args
}

func runOpenCommand(cmd *exec.Cmd, dryRun bool) error {

	// Pretty print the command with proper escaping
	prettyCmd := formatCommand(cmd.Path, cmd.Args)

	if dryRun {
		slog.Debug("Would run command (dry run)", "command", prettyCmd)
		return nil
	} else {
		slog.Debug("Run command", "command", prettyCmd)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	stderrBytes, err := io.ReadAll(stderr)
	if err != nil {
		return fmt.Errorf("error reading stderr: %v", err)
	}

	stdoutBytes, err := io.ReadAll(stdout)
	if err != nil {
		return fmt.Errorf("error reading stdout: %v", err)
	}

	cmdErr := cmd.Wait()

	if len(stderrBytes) > 0 {
		slog.Error("Command returned error", "error", string(stderrBytes))
	}
	if len(stdoutBytes) > 0 {
		slog.Debug("Command returned output", "output", string(stdoutBytes))
	}

	if cmdErr != nil {
		return fmt.Errorf("command failed: %v", cmdErr)
	}

	return nil
}

func shouldOpenInBackground(config BrowserConfig, openInBackgroundByDefault bool) bool {
	if config.OpenInBackground != nil {
		return *config.OpenInBackground
	}
	return openInBackgroundByDefault
}

func buildOpenArgs(config BrowserConfig, openInBackground bool) []string {
	var openArgs []string
	switch config.AppType {
	case "bundleId":
		openArgs = []string{"-b", config.Name}
	default:
		openArgs = []string{"-a", config.Name}
	}

	if openInBackground {
		openArgs = append(openArgs, "-g")
	}

	hasCustomArgs := len(config.Args) > 0
	if hasCustomArgs {
		if !slices.Contains(config.Args, "--args") {
			openArgs = append(openArgs, "--args")
		}
		return append(openArgs, config.Args...)
	}

	return append(openArgs, config.URL)
}

func resolveBrowserProfileArgs(identifier string, profile string) ([]string, bool) {
	var browsersJson []browserInfo
	if err := json.Unmarshal(browsersJsonData, &browsersJson); err != nil {
		slog.Info("Error parsing browsers.json", "error", err)
		return nil, false
	}

	// Try to find matching browser by bundle ID
	var matchedBrowser *browserInfo
	for i := range browsersJson {
		if matchesBrowser(browsersJson[i], identifier) {
			matchedBrowser = &browsersJson[i]
			break
		}
	}

	if matchedBrowser == nil {
		return nil, false
	}

	slog.Debug("Browser found in browsers.json", "identifier", identifier, "type", matchedBrowser.Type)

	if profile != "" {
		switch matchedBrowser.Type {
		case "Chromium":
			homeDir, err := util.UserHomeDir()
			if err != nil {
				slog.Info("Error getting home directory", "error", err)
				return nil, false
			}

			localStatePath := filepath.Join(homeDir, "Library/Application Support", matchedBrowser.ConfigDirRelative, "Local State")
			profilePath, ok := parseProfiles(localStatePath, profile)
			if ok {
				return []string{"--profile-directory=" + profilePath}, true
			}
		case "Firefox":
			homeDir, err := util.UserHomeDir()
			if err != nil {
				slog.Info("Error getting home directory", "error", err)
				return nil, false
			}

			profilesIniPath := filepath.Join(homeDir, "Library/Application Support", matchedBrowser.ConfigDirRelative, "profiles.ini")
			profileName, ok := parseFirefoxProfiles(profilesIniPath, profile)
			if ok {
				return []string{"-P", profileName}, true
			}
		default:
			slog.Info("Browser is not a supported browser type, skipping profile detection", "identifier", identifier)
		}
	}

	return nil, false
}

func matchesBrowser(browser browserInfo, identifier string) bool {
	if browser.ID == identifier || browser.AppName == identifier {
		return true
	}
	return filepath.Ext(identifier) == ".app" && strings.TrimSuffix(filepath.Base(identifier), ".app") == browser.AppName
}

func readFirefoxProfileNames(profilesIniPath string) []string {
	data, err := os.ReadFile(profilesIniPath)
	if err != nil {
		slog.Info("Error reading profiles.ini", "path", profilesIniPath, "error", err)
		return []string{}
	}

	names := []string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if name, ok := strings.CutPrefix(line, "Name="); ok {
			names = append(names, name)
		}
	}
	return names
}

func parseFirefoxProfiles(profilesIniPath string, profile string) (string, bool) {
	names := readFirefoxProfileNames(profilesIniPath)
	for _, name := range names {
		if name == profile {
			return name, true
		}
	}
	slog.Warn("Could not find profile in Firefox profiles.", "Expected profile", profile, "Available profiles", strings.Join(names, ", "))
	return "", false
}

func chromiumInfoCache(localStatePath string) (map[string]interface{}, bool) {
	data, err := os.ReadFile(localStatePath)
	if err != nil {
		slog.Info("Error reading Local State file", "path", localStatePath, "error", err)
		return nil, false
	}

	var localState map[string]interface{}
	if err := json.Unmarshal(data, &localState); err != nil {
		slog.Info("Error parsing Local State JSON", "error", err)
		return nil, false
	}

	profiles, ok := localState["profile"].(map[string]interface{})
	if !ok {
		slog.Info("Could not find profile section in Local State")
		return nil, false
	}

	infoCache, ok := profiles["info_cache"].(map[string]interface{})
	if !ok {
		slog.Info("Could not find info_cache in profile section")
		return nil, false
	}

	return infoCache, true
}

func getAllChromiumProfiles(localStatePath string) []string {
	cache, ok := chromiumInfoCache(localStatePath)
	if !ok {
		return []string{}
	}

	var names []string
	for _, info := range cache {
		profileInfo, ok := info.(map[string]interface{})
		if !ok {
			continue
		}
		name, ok := profileInfo["name"].(string)
		if !ok {
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func parseProfiles(localStatePath string, profile string) (string, bool) {
	infoCache, ok := chromiumInfoCache(localStatePath)
	if !ok {
		return "", false
	}

	// Look for the specified profile
	for profilePath, info := range infoCache {
		profileInfo, ok := info.(map[string]interface{})
		if !ok {
			continue
		}

		name, ok := profileInfo["name"].(string)
		if !ok {
			continue
		}

		if name == profile {
			slog.Info("Found profile by name", "name", name, "path", profilePath)
			return profilePath, true
		}
	}

	// If we didn't find the profile, try to find it by profile folder name
	slog.Debug("Could not find profile in browser profiles, trying to find by profile path", "profile", profile)
	for profilePath, info := range infoCache {
		if profilePath == profile {
			// Try to get the profile name of the profile we want the user to use instead
			if profileInfo, ok := info.(map[string]interface{}); ok {
				if name, ok := profileInfo["name"].(string); ok {
					slog.Warn("Found profile using profile path", "path", profilePath, "name", name, "suggestion", "Please use the profile name instead")
				}
			}
			return profilePath, true
		}
	}

	var profileNames []string
	for _, info := range infoCache {
		profileInfo, ok := info.(map[string]interface{})
		if !ok {
			continue
		}

		name, ok := profileInfo["name"].(string)
		if !ok {
			continue
		}

		profileNames = append(profileNames, name)
	}
	slog.Warn("Could not find profile in browser profiles.", "Expected profile", profile, "Available profiles", strings.Join(profileNames, ", "))

	return "", false
}

// GetProfilesForBrowser returns available profile names for a given browser app name or bundle ID.
// Returns empty slice if browser not in browsers.json, not supported, or profile files are unreadable.
func GetProfilesForBrowser(identifier string) []string {
	var browsersJson []browserInfo
	if err := json.Unmarshal(browsersJsonData, &browsersJson); err != nil {
		slog.Info("Error parsing browsers.json", "error", err)
		return []string{}
	}

	var matchedBrowser *browserInfo
	for i := range browsersJson {
		if matchesBrowser(browsersJson[i], identifier) {
			matchedBrowser = &browsersJson[i]
			break
		}
	}

	if matchedBrowser == nil {
		return []string{}
	}

	homeDir, err := util.UserHomeDir()
	if err != nil {
		slog.Info("Error getting home directory", "error", err)
		return []string{}
	}

	switch matchedBrowser.Type {
	case "Chromium":
		localStatePath := filepath.Join(homeDir, "Library/Application Support", matchedBrowser.ConfigDirRelative, "Local State")
		return getAllChromiumProfiles(localStatePath)
	case "Firefox":
		profilesIniPath := filepath.Join(homeDir, "Library/Application Support", matchedBrowser.ConfigDirRelative, "profiles.ini")
		return readFirefoxProfileNames(profilesIniPath)
	default:
		return []string{}
	}
}

// formatCommand returns a properly shell-escaped string representation of the command
func formatCommand(path string, args []string) string {
	if len(args) == 0 {
		return shellescape.Quote(path)
	}

	quotedArgs := make([]string, len(args))
	for i, arg := range args {
		quotedArgs[i] = shellescape.Quote(arg)
	}

	return strings.Join(quotedArgs, " ")
}
