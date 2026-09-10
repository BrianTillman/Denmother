package cmd

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"github.com/BrianTillman/Denmother/internal/project"
)

//go:embed devassets/*
var devAssets embed.FS

func renderProjectDevCompose(env *devEnvironment) (string, error) {
	root := env.ProjectRoot
	if root == "" {
		root = filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(env.ComposeFile))))
	}
	config := env.ConfigRoot
	if config == "" {
		config = filepath.Join(root, "ha-config")
	}
	settings, err := project.ForConfig(config)
	if err != nil {
		return "", err
	}
	hostRoot, err := devBindRepoRoot(root)
	if err != nil {
		return "", err
	}
	rebase := func(path string) string {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return path
		}
		return filepath.Join(hostRoot, rel)
	}
	haImage := devHomeAssistantImage
	if override := strings.TrimSpace(os.Getenv("DM_DEV_HA_IMAGE")); override != "" {
		// Restrict the image override so it cannot inject YAML into the Compose file.
		if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]*$`).MatchString(override) {
			return "", fmt.Errorf("DM_DEV_HA_IMAGE must be a Docker image reference")
		}
		haImage = override
	}
	data := struct {
		ProjectName, ConfigRoot, DevRoot, ReferencesRoot, AssetsRoot string
		HomeAssistantImage, UIProxyImage, MatterServerImage          string
		HAPort, MatterPort                                           int
	}{env.ProjectName, rebase(config), filepath.Join(hostRoot, ".devcontainer"), rebase(settings.ReferencesDir), rebase(filepath.Join(filepath.Dir(env.ComposeFile), "assets")), haImage, devUIProxyImage, devMatterServerImage, env.HAPort, env.MatterPort}
	content, err := devAssets.ReadFile("devassets/compose.yaml.tmpl")
	if settings.DevComposeTemplate != "" {
		content, err = os.ReadFile(settings.DevComposeTemplate)
	}
	if err != nil {
		return "", fmt.Errorf("load development template: %w", err)
	}
	tmpl, err := template.New("compose").Option("missingkey=error").Funcs(template.FuncMap{
		"join":  filepath.Join,
		"mount": func(source, target, mode string) string { return yamlQuote(bindMount(source, target, mode)) },
	}).Parse(string(content))
	if err != nil {
		return "", err
	}
	var result bytes.Buffer
	if err := tmpl.Execute(&result, data); err != nil {
		return "", err
	}
	return result.String(), nil
}

func writePortableDevAssets(env *devEnvironment) error {
	dir := filepath.Join(filepath.Dir(env.ComposeFile), "assets")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	for _, name := range []string{"prepare.py", "proxy.conf"} {
		data, err := devAssets.ReadFile("devassets/" + name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			return err
		}
	}
	return nil
}
