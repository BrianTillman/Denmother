package skills

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/BrianTillman/Denmother/internal/hatest"
	"gopkg.in/yaml.v3"
)

func TestBundledSkillAndRunnableTestExample(t *testing.T) {
	data, err := Files.ReadFile("denmother/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(data), "---", 3)
	if len(parts) != 3 || parts[0] != "" {
		t.Fatal("missing skill frontmatter")
	}
	var metadata struct {
		Name          string            `yaml:"name"`
		Description   string            `yaml:"description"`
		License       string            `yaml:"license"`
		Compatibility string            `yaml:"compatibility"`
		Metadata      map[string]string `yaml:"metadata"`
	}
	decoder := yaml.NewDecoder(strings.NewReader(parts[1]))
	decoder.KnownFields(true)
	if err := decoder.Decode(&metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Name != "denmother" || len(metadata.Description) == 0 || len(metadata.Description) > 1024 || len(metadata.Compatibility) > 500 {
		t.Fatalf("invalid skill discovery: %+v", metadata)
	}
	if metadata.Metadata["result-schema"] != "dm.operator.v1" || metadata.Metadata["capabilities-schema"] != "dm.capabilities.v1" {
		t.Fatal("missing compatibility metadata")
	}
	links := regexp.MustCompile(`\]\((references/[^)]+)\)`).FindAllStringSubmatch(parts[2], -1)
	if len(links) == 0 {
		t.Fatal("references not discoverable")
	}
	for _, link := range links {
		if _, err := Files.ReadFile("denmother/" + link[1]); err != nil {
			t.Fatal(err)
		}
	}
	guide, err := Files.ReadFile("denmother/references/testing.md")
	if err != nil {
		t.Fatal(err)
	}
	code := strings.SplitN(string(guide), "```yaml\n", 2)
	if len(code) != 2 {
		t.Fatal("no test example")
	}
	file := filepath.Join(t.TempDir(), "skill_test.yaml")
	if err := os.WriteFile(file, []byte(strings.SplitN(code[1], "```", 2)[0]), 0600); err != nil {
		t.Fatal(err)
	}
	spec, err := hatest.LoadTestSpec(file)
	if err != nil {
		t.Fatalf("skill teaches an invalid test: %v", err)
	}
	if len(spec.Tests) != 1 || spec.Tests[0].TraceAssertions == nil {
		t.Fatal("example lacks trace verification")
	}
}
