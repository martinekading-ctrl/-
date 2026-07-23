package main

// The updater uses GitHub Releases because public repositories and release
// downloads are free.  It never stores credentials and only applies an update
// after the user presses the Update button in the app.

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unsafe"
)

const appVersion = "2.11.0"

type updateSettings struct {
	GitHubRepository string `json:"github_repository"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type githubRelease struct {
	TagName string        `json:"tag_name"`
	Name    string        `json:"name"`
	Body    string        `json:"body"`
	Assets  []githubAsset `json:"assets"`
}

type updateInfo struct {
	Available     bool
	Version       string
	Notes         string
	ArchiveURL    string
	ChecksumURL   string
	CheckedAt     time.Time
	Configuration string
}

type updateOutcome struct {
	info       updateInfo
	err        error
	manual     bool
	installing bool
}

var updateOutcomeCh = make(chan updateOutcome, 1)
var githubRepositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func updateButtonText() string {
	if app.updateChecking {
		return "Checking..."
	}
	if app.updateInfo.Available {
		return "Install " + app.updateInfo.Version
	}
	return "Update"
}

func postUpdateOutcome(out updateOutcome) {
	select {
	case updateOutcomeCh <- out:
	default:
		select {
		case <-updateOutcomeCh:
		default:
		}
		updateOutcomeCh <- out
	}
	pPostMessageW.Call(uintptr(app.hwnd), WM_UPDATE_DONE, 0, 0)
}

func startUpdateCheck(manual bool) {
	if app.updateChecking {
		return
	}
	app.updateChecking = true
	app.lastUpdateCheck = time.Now()
	invalidate(false)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		info, err := checkForUpdate(ctx)
		postUpdateOutcome(updateOutcome{info: info, err: err, manual: manual})
	}()
}

func startUpdateInstall() {
	if app.updateChecking || !app.updateInfo.Available {
		return
	}
	app.updateChecking = true
	invalidate(false)
	info := app.updateInfo
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		err := stageUpdate(ctx, info)
		postUpdateOutcome(updateOutcome{info: info, err: err, manual: true, installing: true})
	}()
}

func finishUpdateTask() {
	var out updateOutcome
	select {
	case out = <-updateOutcomeCh:
	default:
		return
	}
	app.updateChecking = false
	if out.err != nil {
		if out.manual {
			app.toast = "Update: " + shortErr(out.err)
			app.toastUntil = time.Now().Add(6 * time.Second)
		}
		if !strings.Contains(out.err.Error(), "Online updates need") {
			addLog("Update check failed: " + shortErr(out.err))
		}
		invalidate(false)
		return
	}
	app.updateInfo = out.info
	if out.installing {
		addLog("Verified update staged. Restarting to install " + out.info.Version)
		pPostMessageW.Call(uintptr(app.hwnd), WM_CLOSE, 0, 0)
		return
	}
	if out.info.Available {
		app.toast = "Update " + out.info.Version + " is ready. Click Install " + out.info.Version
		app.toastUntil = time.Now().Add(8 * time.Second)
		addLog("Update available: " + out.info.Version + " from " + out.info.Configuration)
	} else if out.manual {
		app.toast = "You already have the latest version (" + appVersion + ")"
		app.toastUntil = time.Now().Add(4 * time.Second)
	}
	invalidate(false)
}

func updateConfigPath() string { return filepath.Join(dataDir(), "update_config_v26.json") }

func loadUpdateSettings() updateSettings {
	settings := updateSettings{GitHubRepository: strings.TrimSpace(os.Getenv("MCTR_UPDATE_REPOSITORY"))}
	if b, err := os.ReadFile(updateConfigPath()); err == nil {
		var disk updateSettings
		if json.Unmarshal(b, &disk) == nil && strings.TrimSpace(disk.GitHubRepository) != "" {
			settings.GitHubRepository = strings.TrimSpace(disk.GitHubRepository)
		}
	}
	return settings
}

func updateSetupMessage() string {
	return "Online updates need a public GitHub Releases repository. Set github_repository in update_config_v26.json."
}

func parseVersion(raw string) ([]int, error) {
	s := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(raw, "v"), "V"))
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid version %q", raw)
	}
	out := make([]int, 3)
	for i, p := range parts {
		if p == "" {
			return nil, fmt.Errorf("invalid version %q", raw)
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return nil, fmt.Errorf("invalid version %q", raw)
			}
		}
		_, err := fmt.Sscanf(p, "%d", &out[i])
		if err != nil {
			return nil, fmt.Errorf("invalid version %q", raw)
		}
	}
	return out, nil
}

func versionNewer(candidate, current string) bool {
	a, errA := parseVersion(candidate)
	b, errB := parseVersion(current)
	if errA != nil || errB != nil {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

func releaseAssets(release githubRelease) (githubAsset, githubAsset, error) {
	var archive, checksum githubAsset
	for _, asset := range release.Assets {
		name := strings.ToLower(asset.Name)
		if strings.HasSuffix(name, ".zip") && strings.Contains(name, "windows") && strings.Contains(name, "x64") {
			archive = asset
		}
		if strings.HasSuffix(name, ".sha256") || name == "sha256.txt" {
			checksum = asset
		}
	}
	if archive.BrowserDownloadURL == "" || checksum.BrowserDownloadURL == "" {
		return githubAsset{}, githubAsset{}, errors.New("release must include a Windows x64 zip and SHA256.txt")
	}
	return archive, checksum, nil
}

func checkForUpdate(ctx context.Context) (updateInfo, error) {
	settings := loadUpdateSettings()
	if !githubRepositoryPattern.MatchString(settings.GitHubRepository) {
		return updateInfo{CheckedAt: time.Now()}, errors.New(updateSetupMessage())
	}
	endpoint := "https://api.github.com/repos/" + settings.GitHubRepository + "/releases/latest"
	res, err := fetchURL(ctx, endpoint)
	if err != nil {
		return updateInfo{CheckedAt: time.Now()}, err
	}
	if res.Status == 404 {
		return updateInfo{CheckedAt: time.Now()}, errors.New("no published GitHub release was found")
	}
	if res.Status < 200 || res.Status >= 300 {
		return updateInfo{CheckedAt: time.Now()}, fmt.Errorf("update server returned HTTP %d", res.Status)
	}
	var release githubRelease
	if err := json.Unmarshal(res.Body, &release); err != nil {
		return updateInfo{CheckedAt: time.Now()}, err
	}
	archive, checksum, err := releaseAssets(release)
	if err != nil {
		return updateInfo{CheckedAt: time.Now()}, err
	}
	info := updateInfo{
		Available:     versionNewer(release.TagName, appVersion),
		Version:       strings.TrimPrefix(release.TagName, "v"),
		Notes:         strings.TrimSpace(release.Body),
		ArchiveURL:    archive.BrowserDownloadURL,
		ChecksumURL:   checksum.BrowserDownloadURL,
		CheckedAt:     time.Now(),
		Configuration: settings.GitHubRepository,
	}
	if info.Version == "" {
		return updateInfo{CheckedAt: time.Now()}, errors.New("release tag has no version")
	}
	return info, nil
}

func extractChecksum(body []byte) (string, error) {
	for _, field := range strings.Fields(string(body)) {
		if len(field) != 64 {
			continue
		}
		if _, err := hex.DecodeString(field); err == nil {
			return strings.ToLower(field), nil
		}
	}
	return "", errors.New("the checksum file does not contain a SHA-256 value")
}

func stageUpdate(ctx context.Context, info updateInfo) error {
	checksumResponse, err := fetchURL(ctx, info.ChecksumURL)
	if err != nil {
		return err
	}
	if checksumResponse.Status < 200 || checksumResponse.Status >= 300 {
		return fmt.Errorf("checksum download returned HTTP %d", checksumResponse.Status)
	}
	expected, err := extractChecksum(checksumResponse.Body)
	if err != nil {
		return err
	}
	archiveResponse, err := fetchURL(ctx, info.ArchiveURL)
	if err != nil {
		return err
	}
	if archiveResponse.Status < 200 || archiveResponse.Status >= 300 {
		return fmt.Errorf("update download returned HTTP %d", archiveResponse.Status)
	}
	if len(archiveResponse.Body) == 0 || len(archiveResponse.Body) > 250*1024*1024 {
		return errors.New("update archive is empty or too large")
	}
	actualBytes := sha256.Sum256(archiveResponse.Body)
	actual := hex.EncodeToString(actualBytes[:])
	if !strings.EqualFold(actual, expected) {
		return errors.New("update checksum mismatch; the download was discarded")
	}

	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	stageDir := filepath.Join(dataDir(), "update_staging", info.Version)
	if err := os.RemoveAll(stageDir); err != nil {
		return err
	}
	if err := os.MkdirAll(stageDir, 0700); err != nil {
		return err
	}
	archivePath := filepath.Join(stageDir, "release.zip")
	if err := os.WriteFile(archivePath, archiveResponse.Body, 0600); err != nil {
		return err
	}
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer zr.Close()
	targetName := strings.ToLower(filepath.Base(exePath))
	var executable *zip.File
	for _, f := range zr.File {
		name := strings.ToLower(filepath.Base(f.Name))
		if name == targetName || (executable == nil && strings.HasSuffix(name, ".exe")) {
			executable = f
			if name == targetName {
				break
			}
		}
	}
	if executable == nil || executable.FileInfo().IsDir() {
		return errors.New("update zip does not contain an executable")
	}
	in, err := executable.Open()
	if err != nil {
		return err
	}
	defer in.Close()
	stagedExe := filepath.Join(stageDir, filepath.Base(exePath))
	out, err := os.OpenFile(stagedExe, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return launchUpdateScript(exePath, stagedExe, stageDir)
}

func launchUpdateScript(exePath, stagedExe, stageDir string) error {
	quote := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
	scriptPath := filepath.Join(stageDir, "apply-update.cmd")
	script := "@echo off\r\n" +
		"timeout /t 2 /nobreak >nul\r\n" +
		"copy /y " + quote(stagedExe) + " " + quote(exePath) + " >nul\r\n" +
		"start \"\" " + quote(exePath) + "\r\n" +
		"rmdir /s /q " + quote(stageDir) + "\r\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0700); err != nil {
		return err
	}
	result, _, callErr := pShellExecuteW.Call(0, uintptr(unsafe.Pointer(utf16Ptr("open"))), uintptr(unsafe.Pointer(utf16Ptr("cmd.exe"))), uintptr(unsafe.Pointer(utf16Ptr("/c "+quote(scriptPath)))), 0, SW_SHOWNORMAL)
	if result <= 32 {
		return fmt.Errorf("could not start updater: %v", callErr)
	}
	return nil
}
