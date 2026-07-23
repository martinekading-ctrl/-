package main

import (
	"strings"
	"testing"
)

func TestVersionNewer(t *testing.T) {
	for _, tc := range []struct {
		candidate string
		current   string
		want      bool
	}{
		{"v2.6.1", "2.6.0", true},
		{"2.7.0", "2.6.9", true},
		{"2.6.0", "2.6.0", false},
		{"2.5.9", "2.6.0", false},
		{"latest", "2.6.0", false},
	} {
		if got := versionNewer(tc.candidate, tc.current); got != tc.want {
			t.Fatalf("versionNewer(%q, %q)=%v, want %v", tc.candidate, tc.current, got, tc.want)
		}
	}
}

func TestExtractChecksum(t *testing.T) {
	value := "a2f3c4d5e6f70809101112131415161718191a1b1c1d1e1f2021222324252627"
	got, err := extractChecksum([]byte(value + "  MultiChainTokenRadar_Windows_x64.zip"))
	if err != nil || got != value {
		t.Fatalf("checksum parse failed: got=%q err=%v", got, err)
	}
}

func TestUpdateScriptRetainsBackupAndRetriesFileLocks(t *testing.T) {
	script := buildUpdateScript(`C:\\Program Files\\Radar\\Radar.exe`, `C:\\Users\\u\\AppData\\Local\\Radar\\staged.exe`, `C:\\Users\\u\\AppData\\Local\\Radar\\stage`)
	for _, want := range []string{
		".preupdate.bak",
		":backup_retry",
		":replace",
		"if %RETRY% GEQ 10 goto restore",
		"copy /y \"%BACKUP%\" \"%TARGET%\"",
		"start \"\" \"%TARGET%\"",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("update script missing %q:\n%s", want, script)
		}
	}
}
