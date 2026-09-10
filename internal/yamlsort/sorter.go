package yamlsort

import (
	"bytes"
	"os"
	"sort"

	"gopkg.in/yaml.v3"
)

// Sorter sorts YAML files by a configurable key
type Sorter struct {
	opts Options
}

// NewSorter creates a new Sorter with the given options
func NewSorter(opts Options) *Sorter {
	if opts.SortKey == "" {
		opts.SortKey = "unique_id"
	}
	return &Sorter{opts: opts}
}

// SortFile sorts a single YAML file
func (s *Sorter) SortFile(path string) (*Result, error) {
	result := &Result{FilePath: path}

	content, err := os.ReadFile(path)
	if err != nil {
		result.Error = err
		return result, err
	}

	// Parse into Node tree (preserves comments)
	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		result.Error = err
		return result, err
	}

	wasSorted, itemCount := s.sortDocument(&root)
	result.WasSorted = wasSorted
	result.ItemCount = itemCount

	if s.opts.CheckOnly || wasSorted {
		result.Modified = false
		return result, nil
	}

	if err := s.writeNode(path, &root); err != nil {
		result.Error = err
		return result, err
	}
	result.Modified = true

	return result, nil
}

// SortContent sorts YAML content and returns the sorted string
func (s *Sorter) SortContent(content string) (string, bool, error) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(content), &root); err != nil {
		return "", false, err
	}

	wasSorted, _ := s.sortDocument(&root)

	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(&root); err != nil {
		return "", false, err
	}
	encoder.Close()

	return buf.String(), wasSorted, nil
}

// sortDocument sorts the document and returns whether it was already sorted
func (s *Sorter) sortDocument(node *yaml.Node) (wasSorted bool, itemCount int) {
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		return s.sortDocument(node.Content[0])
	}

	// Only sort sequence nodes (YAML lists)
	if node.Kind != yaml.SequenceNode {
		return true, 0
	}

	items := node.Content
	itemCount = len(items)

	if itemCount == 0 {
		return true, 0
	}

	type sortItem struct {
		node *yaml.Node
		key  string
	}
	sortItems := make([]sortItem, len(items))
	for i, item := range items {
		sortItems[i] = sortItem{
			node: item,
			key:  s.extractSortKey(item),
		}
	}

	wasSorted = sort.SliceIsSorted(sortItems, func(i, j int) bool {
		return sortItems[i].key < sortItems[j].key
	})

	if !wasSorted {
		sort.SliceStable(sortItems, func(i, j int) bool {
			return sortItems[i].key < sortItems[j].key
		})

		for i, si := range sortItems {
			node.Content[i] = si.node
		}
	}

	if s.opts.SortEntities {
		for _, item := range node.Content {
			s.sortEntitiesList(item)
		}
	}

	return wasSorted, itemCount
}

// extractSortKey extracts the sort key from a mapping node
func (s *Sorter) extractSortKey(node *yaml.Node) string {
	if node.Kind != yaml.MappingNode {
		return ""
	}

	// Try configured key first, then fallbacks
	keyOrder := []string{s.opts.SortKey}
	if s.opts.SortKey != "id" {
		keyOrder = append(keyOrder, "id")
	}
	if s.opts.SortKey != "alias" {
		keyOrder = append(keyOrder, "alias")
	}
	if s.opts.SortKey != "name" {
		keyOrder = append(keyOrder, "name")
	}

	for _, key := range keyOrder {
		if val := s.getMappingValue(node, key); val != "" {
			return val
		}
	}
	return ""
}

// getMappingValue gets the string value of a key from a mapping node
func (s *Sorter) getMappingValue(node *yaml.Node, key string) string {
	if node.Kind != yaml.MappingNode {
		return ""
	}

	// Mapping nodes have key-value pairs in Content array: [key1, val1, key2, val2, ...]
	for i := 0; i < len(node.Content)-1; i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1].Value
		}
	}
	return ""
}

// sortEntitiesList sorts the entities list within a mapping node
func (s *Sorter) sortEntitiesList(node *yaml.Node) {
	if node.Kind != yaml.MappingNode {
		return
	}

	for i := 0; i < len(node.Content)-1; i += 2 {
		if node.Content[i].Value == "entities" {
			entitiesNode := node.Content[i+1]
			if entitiesNode.Kind == yaml.SequenceNode {
				sort.SliceStable(entitiesNode.Content, func(a, b int) bool {
					return entitiesNode.Content[a].Value < entitiesNode.Content[b].Value
				})
			}
			break
		}
	}
}

// writeNode writes a yaml.Node back to a file
func (s *Sorter) writeNode(path string, node *yaml.Node) error {
	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)

	if err := encoder.Encode(node); err != nil {
		return err
	}
	encoder.Close()

	return os.WriteFile(path, buf.Bytes(), 0644)
}
