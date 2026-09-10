package yamlsort

// Options configures the YAML sorter behavior
type Options struct {
	// SortKey is the key to sort list items by (default: "unique_id")
	SortKey string
	// SortEntities sorts entity lists within groups alphabetically
	SortEntities bool
	// CheckOnly reports unsorted files without modifying them
	CheckOnly bool
	// Verbose enables detailed output
	Verbose bool
}

// DefaultOptions sorts by unique_id and alphabetizes entity lists.
func DefaultOptions() Options {
	return Options{
		SortKey:      "unique_id",
		SortEntities: true,
		CheckOnly:    false,
		Verbose:      false,
	}
}

// Result represents the outcome of sorting a single file
type Result struct {
	// FilePath is the path to the processed file
	FilePath string
	// WasSorted reports whether the top-level list was already ordered by SortKey.
	WasSorted bool
	// Modified is true if the file was changed
	Modified bool
	// ItemCount is the number of list items processed
	ItemCount int
	// Error is any error that occurred
	Error error
}

// Results aggregates results from sorting multiple files
type Results struct {
	// Sorted contains files that need sorting, including unwritten CheckOnly results.
	Sorted []string
	// Unchanged contains files that were already sorted
	Unchanged []string
	// Skipped contains empty lists and documents without a top-level list.
	Skipped []string
	// Errors contains files that had errors
	Errors map[string]error
}

// NewResults creates an empty Results
func NewResults() *Results {
	return &Results{
		Sorted:    []string{},
		Unchanged: []string{},
		Skipped:   []string{},
		Errors:    make(map[string]error),
	}
}

// Add adds a single result to the aggregate
func (r *Results) Add(result *Result) {
	if result.Error != nil {
		r.Errors[result.FilePath] = result.Error
		return
	}
	if result.ItemCount == 0 {
		r.Skipped = append(r.Skipped, result.FilePath)
		return
	}
	if result.WasSorted {
		r.Unchanged = append(r.Unchanged, result.FilePath)
	} else {
		r.Sorted = append(r.Sorted, result.FilePath)
	}
}

// HasErrors returns true if any files had errors
func (r *Results) HasErrors() bool {
	return len(r.Errors) > 0
}

// NeedsSorting returns true if any files need sorting (for check mode)
func (r *Results) NeedsSorting() bool {
	return len(r.Sorted) > 0
}

// TotalProcessed counts sorted, unchanged, and skipped files, excluding errors.
func (r *Results) TotalProcessed() int {
	return len(r.Sorted) + len(r.Unchanged) + len(r.Skipped)
}
