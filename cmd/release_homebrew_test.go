package cmd

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func TestHomebrewFormulaSelectsMatchingReleaseArchives(t *testing.T) {
	for _, version := range []string{"1.2.3", "0.1.0-rc.1"} {
		t.Run(version, func(t *testing.T) {
			checksums := homebrewTestChecksums(version)
			formula, err := renderHomebrewFormula(version, "example/denmother", checksums)
			if err != nil {
				t.Fatal(err)
			}
			// Match each nested OS/CPU selection to its URL and digest together;
			// swapping architectures must fail even if all hashes still appear.
			pattern := regexp.MustCompile(`(?s)on_(macos|linux) do\s+on_(arm|intel) do\s+url "([^"]+)"\s+sha256 "([^"]+)"`)
			matches := pattern.FindAllStringSubmatch(string(formula), -1)
			if len(matches) != 4 {
				t.Fatalf("expected four platform selections, got %d", len(matches))
			}
			seen := make(map[string]bool)
			for _, match := range matches {
				os := map[string]string{"macos": "darwin", "linux": "linux"}[match[1]]
				arch := map[string]string{"arm": "arm64", "intel": "amd64"}[match[2]]
				name := fmt.Sprintf("denmother-%s-%s-%s.tar.gz", version, os, arch)
				if seen[name] || match[3] != "https://github.com/example/denmother/releases/download/v"+version+"/"+name || match[4] != checksums[name] {
					t.Fatalf("incorrect archive selection: %v", match)
				}
				seen[name] = true
			}
			if !strings.Contains(string(formula), `version "`+version+`"`) {
				t.Fatal("formula must preserve the explicit release version")
			}
		})
	}
}

func TestHomebrewFormulaRejectsUnsafeMetadataAndMissingArtifacts(t *testing.T) {
	for _, repository := range []string{"", "denmother", "https://github.com/example/denmother", "../denmother", "example/..", "example/repo/subdir", "example/repo?token=secret", "example/repo\n", `example/#{system('id')}`} {
		t.Run(repository, func(t *testing.T) {
			if _, err := renderHomebrewFormula("1.2.3", repository, homebrewTestChecksums("1.2.3")); err == nil {
				t.Fatal("accepted invalid repository")
			}
		})
	}
	if _, err := renderHomebrewFormula(`1.2.3#{system('id')}`, "example/denmother", nil); err == nil {
		t.Fatal("accepted invalid version")
	}
	for _, digest := range []string{"", "not-a-sha256", strings.Repeat("a", 63), strings.Repeat("a", 64) + "\n"} {
		checksums := homebrewTestChecksums("1.2.3")
		checksums["denmother-1.2.3-linux-arm64.tar.gz"] = digest
		if _, err := renderHomebrewFormula("1.2.3", "example/denmother", checksums); err == nil {
			t.Fatalf("accepted missing/invalid digest %q", digest)
		}
	}
}

func homebrewTestChecksums(version string) map[string]string {
	checksums := make(map[string]string)
	for _, platform := range []string{"darwin-arm64", "darwin-amd64", "linux-arm64", "linux-amd64", "windows-amd64"} {
		name := fmt.Sprintf("denmother-%s-%s.tar.gz", version, platform)
		checksums[name] = fmt.Sprintf("%x", sha256.Sum256([]byte(name)))
	}
	return checksums
}
