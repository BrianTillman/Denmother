package yamlsort

import (
	"strings"
	"testing"
)

func TestSortByUniqueID(t *testing.T) {
	input := `- platform: group
  unique_id: zebra
  name: Zebra
- platform: group
  unique_id: alpha
  name: Alpha
- platform: group
  unique_id: middle
  name: Middle
`

	sorter := NewSorter(DefaultOptions())
	result, wasSorted, err := sorter.SortContent(input)
	if err != nil {
		t.Fatalf("SortContent failed: %v", err)
	}

	if wasSorted {
		t.Error("Expected wasSorted=false for unsorted input")
	}

	alphaIdx := strings.Index(result, "unique_id: alpha")
	middleIdx := strings.Index(result, "unique_id: middle")
	zebraIdx := strings.Index(result, "unique_id: zebra")

	if alphaIdx == -1 || middleIdx == -1 || zebraIdx == -1 {
		t.Fatalf("Missing unique_id in output:\n%s", result)
	}

	if alphaIdx > middleIdx || middleIdx > zebraIdx {
		t.Errorf("Items not sorted correctly. alpha=%d, middle=%d, zebra=%d\nOutput:\n%s",
			alphaIdx, middleIdx, zebraIdx, result)
	}
}

func TestAlreadySorted(t *testing.T) {
	input := `- platform: group
  unique_id: alpha
  name: Alpha
- platform: group
  unique_id: beta
  name: Beta
- platform: group
  unique_id: gamma
  name: Gamma
`

	sorter := NewSorter(DefaultOptions())
	_, wasSorted, err := sorter.SortContent(input)
	if err != nil {
		t.Fatalf("SortContent failed: %v", err)
	}

	if !wasSorted {
		t.Error("Expected wasSorted=true for already sorted input")
	}
}

func TestSortEntities(t *testing.T) {
	input := `- platform: group
  unique_id: lights
  entities:
    - light.z_light
    - light.a_light
    - light.m_light
`

	opts := DefaultOptions()
	opts.SortEntities = true
	sorter := NewSorter(opts)

	result, _, err := sorter.SortContent(input)
	if err != nil {
		t.Fatalf("SortContent failed: %v", err)
	}

	aIdx := strings.Index(result, "light.a_light")
	mIdx := strings.Index(result, "light.m_light")
	zIdx := strings.Index(result, "light.z_light")

	if aIdx == -1 || mIdx == -1 || zIdx == -1 {
		t.Fatalf("Missing entity in output:\n%s", result)
	}

	if aIdx > mIdx || mIdx > zIdx {
		t.Errorf("Entities not sorted correctly. a=%d, m=%d, z=%d\nOutput:\n%s",
			aIdx, mIdx, zIdx, result)
	}
}

func TestFallbackToID(t *testing.T) {
	input := `- id: zebra_auto
  alias: Zebra Automation
- id: alpha_auto
  alias: Alpha Automation
`

	opts := DefaultOptions()
	opts.SortKey = "id"
	sorter := NewSorter(opts)

	result, wasSorted, err := sorter.SortContent(input)
	if err != nil {
		t.Fatalf("SortContent failed: %v", err)
	}

	if wasSorted {
		t.Error("Expected wasSorted=false for unsorted input")
	}

	alphaIdx := strings.Index(result, "id: alpha_auto")
	zebraIdx := strings.Index(result, "id: zebra_auto")

	if alphaIdx > zebraIdx {
		t.Errorf("Items not sorted by id. alpha=%d, zebra=%d\nOutput:\n%s",
			alphaIdx, zebraIdx, result)
	}
}

func TestFallbackKeyOrder(t *testing.T) {
	// Test that when unique_id is missing, it falls back to id, then alias, then name
	input := `- name: Zebra
- name: Alpha
`

	sorter := NewSorter(DefaultOptions())
	result, wasSorted, err := sorter.SortContent(input)
	if err != nil {
		t.Fatalf("SortContent failed: %v", err)
	}

	if wasSorted {
		t.Error("Expected wasSorted=false for unsorted input")
	}

	alphaIdx := strings.Index(result, "name: Alpha")
	zebraIdx := strings.Index(result, "name: Zebra")

	if alphaIdx > zebraIdx {
		t.Errorf("Items not sorted by fallback name. alpha=%d, zebra=%d\nOutput:\n%s",
			alphaIdx, zebraIdx, result)
	}
}

func TestEmptyList(t *testing.T) {
	input := `[]
`

	sorter := NewSorter(DefaultOptions())
	_, wasSorted, err := sorter.SortContent(input)
	if err != nil {
		t.Fatalf("SortContent failed: %v", err)
	}

	if !wasSorted {
		t.Error("Expected wasSorted=true for empty list")
	}
}

func TestNonListYAML(t *testing.T) {
	input := `key: value
another: item
`

	sorter := NewSorter(DefaultOptions())
	_, wasSorted, err := sorter.SortContent(input)
	if err != nil {
		t.Fatalf("SortContent failed: %v", err)
	}

	// Non-list YAML should be considered "already sorted" (nothing to sort)
	if !wasSorted {
		t.Error("Expected wasSorted=true for non-list YAML")
	}
}

func TestPreservesStructure(t *testing.T) {
	input := `- platform: group
  unique_id: beta
  name: Beta Group
  entities:
    - light.beta_one
    - light.beta_two
- platform: group
  unique_id: alpha
  name: Alpha Group
  entities:
    - light.alpha_one
`

	sorter := NewSorter(DefaultOptions())
	result, _, err := sorter.SortContent(input)
	if err != nil {
		t.Fatalf("SortContent failed: %v", err)
	}

	if !strings.Contains(result, "unique_id: alpha") {
		t.Error("Missing alpha unique_id")
	}
	if !strings.Contains(result, "name: Alpha Group") {
		t.Error("Missing Alpha Group name")
	}
	if !strings.Contains(result, "light.alpha_one") {
		t.Error("Missing alpha entity")
	}

	alphaIdx := strings.Index(result, "unique_id: alpha")
	betaIdx := strings.Index(result, "unique_id: beta")
	if alphaIdx > betaIdx {
		t.Errorf("Alpha should come before beta. alpha=%d, beta=%d", alphaIdx, betaIdx)
	}
}

func TestResultsAggregation(t *testing.T) {
	results := NewResults()

	results.Add(&Result{FilePath: "sorted.yaml", WasSorted: false, ItemCount: 3})

	results.Add(&Result{FilePath: "unchanged.yaml", WasSorted: true, ItemCount: 2})

	results.Add(&Result{FilePath: "skipped.yaml", WasSorted: true, ItemCount: 0})

	results.Add(&Result{FilePath: "error.yaml", Error: errTest})

	if len(results.Sorted) != 1 {
		t.Errorf("Expected 1 sorted file, got %d", len(results.Sorted))
	}
	if len(results.Unchanged) != 1 {
		t.Errorf("Expected 1 unchanged file, got %d", len(results.Unchanged))
	}
	if len(results.Skipped) != 1 {
		t.Errorf("Expected 1 skipped file, got %d", len(results.Skipped))
	}
	if len(results.Errors) != 1 {
		t.Errorf("Expected 1 error, got %d", len(results.Errors))
	}

	if !results.HasErrors() {
		t.Error("Expected HasErrors() to be true")
	}
	if !results.NeedsSorting() {
		t.Error("Expected NeedsSorting() to be true")
	}
	if results.TotalProcessed() != 3 {
		t.Errorf("Expected TotalProcessed() to be 3, got %d", results.TotalProcessed())
	}
}

var errTest = &testError{}

type testError struct{}

func (e *testError) Error() string { return "test error" }
