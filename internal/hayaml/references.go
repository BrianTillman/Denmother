// Package hayaml extracts static Home Assistant references from YAML syntax trees.
package hayaml

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

type Reference struct {
	Kind   string
	Reason string
	Entity string
	Line   int
	Column int
}

type References struct {
	Static  []Reference
	Dynamic []Reference
}

var entityID = regexp.MustCompile(`^[a-z_]+\.[a-z0-9_]+$`)

// ExtractReferences recognizes scalar/list targets, scene entity maps, dashboard
// entities, and blueprint inputs. It does not evaluate Jinja or !input values.
// Parse errors are returned; malformed YAML is never a successful empty scan.
func ExtractReferences(data []byte) (References, error) {
	var result References
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var walk func(*yaml.Node, bool, bool) error
	active := map[*yaml.Node]bool{}
	seen := map[string]bool{}
	add := func(node *yaml.Node, dynamic bool) {
		key := fmt.Sprintf("%d:%d:%t", node.Line, node.Column, dynamic)
		if seen[key] {
			return
		}
		seen[key] = true
		ref := Reference{Entity: node.Value, Line: node.Line, Column: node.Column}
		if dynamic {
			switch {
			case node.Tag == "!input":
				ref.Kind = "blueprint_input"
				ref.Reason = "Blueprint input requires its consuming automation's runtime input values."
			case strings.Contains(node.Value, "{{") || strings.Contains(node.Value, "{%"):
				ref.Kind = "template"
				ref.Reason = "Jinja entity target requires Home Assistant runtime variables and template evaluation."
			default:
				ref.Kind = "yaml_tag"
				ref.Reason = "Tagged entity target requires Home Assistant YAML loading before runtime resolution."
			}
			result.Dynamic = append(result.Dynamic, ref)
		} else {
			result.Static = append(result.Static, ref)
		}
	}
	walk = func(node *yaml.Node, entityContext, inputContext bool) error {
		if node == nil {
			return nil
		}
		if active[node] {
			return fmt.Errorf("line %d: cyclic YAML alias", node.Line)
		}
		active[node] = true
		defer delete(active, node)
		switch node.Kind {
		case yaml.DocumentNode:
			for _, child := range node.Content {
				if err := walk(child, false, false); err != nil {
					return err
				}
			}
		case yaml.AliasNode:
			return walk(node.Alias, entityContext, inputContext)
		case yaml.SequenceNode:
			for _, child := range node.Content {
				if err := walk(child, entityContext, inputContext); err != nil {
					return err
				}
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(node.Content); i += 2 {
				key, value := node.Content[i], node.Content[i+1]
				if value.Kind == yaml.ScalarNode {
					switch key.Value {
					case "service", "action", "description", "name", "alias", "icon", "path", "id", "unique_id", "state", "from", "to":
						continue
					}
				}
				if entityContext && entityID.MatchString(key.Value) {
					add(key, false)
				}
				isEntity := false
				switch key.Value {
				case "entity_id", "entity", "entities", "scene", "include_entities", "exclude_entities", "trigger_entity_id":
					isEntity = true
				}
				// Values inside use_blueprint.input may use arbitrary input names.
				isInput := inputContext || key.Value == "input"
				if err := walk(value, isEntity, isInput); err != nil {
					return err
				}
			}
		case yaml.ScalarNode:
			if !entityContext && !inputContext {
				return nil
			}
			if strings.Contains(node.Value, "{{") || strings.Contains(node.Value, "{%") || strings.HasPrefix(node.Tag, "!") && !strings.HasPrefix(node.Tag, "!!") {
				add(node, true)
			} else if entityID.MatchString(node.Value) {
				add(node, false)
			}
		}
		return nil
	}
	for {
		var doc yaml.Node
		if err := decoder.Decode(&doc); err == io.EOF {
			break
		} else if err != nil {
			return result, err
		}
		if err := walk(&doc, false, false); err != nil {
			return result, err
		}
	}
	return result, nil
}
