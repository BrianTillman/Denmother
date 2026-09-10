package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPortableDevComposeIsIsolatedAndUsesSelectedConfig(t *testing.T) {
	root := t.TempDir()
	env := &devEnvironment{ProjectName: "hass-dev-example", ProjectRoot: root, ConfigRoot: filepath.Join(root, "example-config"), ComposeFile: filepath.Join(root, ".devcontainer/worktrees/example/docker-compose.yml"), HAPort: 8123}
	rendered, err := renderDevCompose(env)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Services map[string]struct {
			Ports       []string `yaml:"ports"`
			Volumes     []string `yaml:"volumes"`
			NetworkMode string   `yaml:"network_mode"`
			Privileged  bool     `yaml:"privileged"`
		} `yaml:"services"`
		Networks map[string]struct {
			Internal bool `yaml:"internal"`
		} `yaml:"networks"`
	}
	if err := yaml.Unmarshal([]byte(rendered), &document); err != nil {
		t.Fatal(err)
	}
	if !document.Networks["homeassistant"].Internal {
		t.Fatal("HA must have no outbound network")
	}
	for name, service := range document.Services {
		if service.NetworkMode == "host" || service.Privileged {
			t.Fatalf("%s escaped isolation", name)
		}
		for _, port := range service.Ports {
			if !strings.HasPrefix(port, "127.0.0.1:") {
				t.Fatalf("%s exposed non-loopback port %s", name, port)
			}
		}
	}
	if !strings.Contains(rendered, filepath.ToSlash(env.ConfigRoot)+":/ha-config-source:ro") {
		t.Fatal("selected config not mounted read-only")
	}
	if strings.Contains(rendered, "/init-storage.sh") {
		t.Fatal("portable runtime depends on parent bootstrap")
	}
}

func TestRenderDevComposeUsesDockerHostRepoRoot(t *testing.T) {
	root := t.TempDir()
	hostRoot := filepath.Join(string(filepath.Separator), "docker-host", "workspace", "example")
	t.Setenv("DM_DEV_HOST_REPO_ROOT", hostRoot)
	env := &devEnvironment{ProjectRoot: root, ConfigRoot: filepath.Join(root, "ha-config"), ProjectName: "hass-dev-example", ComposeFile: filepath.Join(root, ".devcontainer/worktrees/example/docker-compose.yml"), HAPort: 8123}
	rendered, err := renderDevCompose(env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, filepath.ToSlash(filepath.Join(hostRoot, "ha-config"))+":/ha-config-source:ro") {
		t.Fatal("Docker-host root not applied")
	}
	t.Setenv("DM_DEV_HOST_REPO_ROOT", "relative/path")
	if _, err := renderDevCompose(env); err == nil {
		t.Fatal("relative Docker-host root must fail")
	}
}

func TestDevComposeUsesExplicitProjectTemplate(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".denmother.yaml"), []byte("version: 1\nconfig_dir: ha-config\ndev_compose_template: custom.tmpl\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "custom.tmpl"), []byte("name: {{ .ProjectName }}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	env := &devEnvironment{ProjectRoot: root, ConfigRoot: filepath.Join(root, "ha-config"), ProjectName: "custom-project", ComposeFile: filepath.Join(root, ".devcontainer/worktrees/custom/docker-compose.yml")}
	got, err := renderDevCompose(env)
	if err != nil || got != "name: custom-project\n" {
		t.Fatalf("custom template: %q %v", got, err)
	}
}
