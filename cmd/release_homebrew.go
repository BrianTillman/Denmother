package cmd

import (
	"bytes"
	_ "embed"
	"fmt"
	"regexp"
	"text/template"
)

//go:embed releaseassets/denmother.rb.tmpl
var homebrewFormulaTemplate string

func validateHomebrewRepository(repository string) error {
	// Restrict interpolation to GitHub repository names, excluding Ruby syntax,
	// URL credentials, queries, and path traversal.
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9_][A-Za-z0-9_.-]*$`).MatchString(repository) {
		return fmt.Errorf("--homebrew-repository must be a GitHub OWNER/REPO name")
	}
	return nil
}

func renderHomebrewFormula(version, repository string, checksums map[string]string) ([]byte, error) {
	if !releaseVersionPattern.MatchString(version) {
		return nil, fmt.Errorf("invalid Homebrew release version")
	}
	if err := validateHomebrewRepository(repository); err != nil {
		return nil, err
	}
	type platform struct{ OS, Arch, URL, SHA256 string }
	data := struct {
		Version, Homepage string
		Platforms         []platform
	}{Version: version, Homepage: "https://github.com/" + repository}
	for _, target := range []struct{ os, arch, brewOS, brewArch string }{
		{"darwin", "arm64", "macos", "arm"},
		{"darwin", "amd64", "macos", "intel"},
		{"linux", "arm64", "linux", "arm"},
		{"linux", "amd64", "linux", "intel"},
	} {
		name := fmt.Sprintf("denmother-%s-%s-%s.tar.gz", version, target.os, target.arch)
		digest := checksums[name]
		if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(digest) {
			return nil, fmt.Errorf("missing or invalid SHA256 for Homebrew archive %s", name)
		}
		data.Platforms = append(data.Platforms, platform{target.brewOS, target.brewArch,
			data.Homepage + "/releases/download/v" + version + "/" + name, digest})
	}
	tmpl, err := template.New("denmother.rb").Parse(homebrewFormulaTemplate)
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	if err := tmpl.Execute(&output, data); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
