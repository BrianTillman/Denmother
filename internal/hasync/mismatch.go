package hasync

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BrianTillman/Denmother/internal/hayaml"
	"github.com/BrianTillman/Denmother/internal/project"

	"github.com/BrianTillman/Denmother/internal/util"
	"github.com/fatih/color"
)

// EntityMismatch represents a mismatch between config and live entities
type EntityMismatch struct {
	ConfigEntity    string
	SuggestedEntity string
	MismatchType    string // "missing", "name_variation", "typo"
	Confidence      float64
	FilePath        string
	LineNumber      int
}

// MismatchAnalysis holds the results of mismatch analysis
type MismatchAnalysis struct {
	TotalConfigRefs   int
	TotalLiveEntities int
	ExactMatches      int
	MissingEntities   []string
	Mismatches        []EntityMismatch
}

// MismatchReport contains the full mismatch report
type MismatchReport struct {
	Analysis   *MismatchAnalysis
	ReportPath string
}

var configPaths = []string{
	"configuration.yaml",
	"automations",
	"automations.yaml",
	"scenes",
	"scenes.yaml",
	"homekit",
	"helpers",
	"dashboards",
	"customize.yaml",
	"scripts.yaml",
	"scripts",
	"packages",
}

// AnalyzeMismatches compares config entities against live entities
func AnalyzeMismatches(configPath string, liveEntities map[string]bool) (*MismatchAnalysis, error) {
	analysis := &MismatchAnalysis{
		TotalLiveEntities: len(liveEntities),
	}

	configEntities := make(map[string][]entityLocation)
	for _, p := range configPaths {
		fullPath := filepath.Join(configPath, p)
		if !util.FileExists(fullPath) && !util.DirExists(fullPath) {
			continue
		}

		files := getYAMLFilesForMismatch(fullPath)
		for _, file := range files {
			refs, err := extractConfigEntities(file, configPath)
			if err != nil {
				return nil, err
			}
			for entity, locs := range refs {
				configEntities[entity] = append(configEntities[entity], locs...)
			}
		}
	}

	analysis.TotalConfigRefs = len(configEntities)

	for entity, locations := range configEntities {
		if liveEntities[entity] {
			analysis.ExactMatches++
		} else {
			analysis.MissingEntities = append(analysis.MissingEntities, entity)

			suggestion, confidence := findBestMatch(entity, liveEntities)
			if suggestion != "" {
				mismatchType := "name_variation"
				if confidence > 0.9 {
					mismatchType = "typo"
				}

				loc := locations[0]
				analysis.Mismatches = append(analysis.Mismatches, EntityMismatch{
					ConfigEntity:    entity,
					SuggestedEntity: suggestion,
					MismatchType:    mismatchType,
					Confidence:      confidence,
					FilePath:        loc.file,
					LineNumber:      loc.line,
				})
			}
		}
	}

	sort.Slice(analysis.Mismatches, func(i, j int) bool {
		return analysis.Mismatches[i].Confidence > analysis.Mismatches[j].Confidence
	})

	return analysis, nil
}

// GenerateMismatchReport creates a mismatch report and saves it
func GenerateMismatchReport(configPath, haURL string, analysis *MismatchAnalysis) (*MismatchReport, error) {
	settings, err := project.ForConfig(configPath)
	if err != nil {
		return nil, err
	}
	refDir := settings.ReferencesDir
	if err := os.MkdirAll(refDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create reference directory: %w", err)
	}

	reportPath := filepath.Join(refDir, "mismatch-report.txt")
	writer := &bytes.Buffer{}

	fmt.Fprintln(writer, "ENTITY CONFIGURATION MISMATCH ANALYSIS")
	fmt.Fprintln(writer, strings.Repeat("=", 60))
	fmt.Fprintf(writer, "Analysis Date: %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(writer, "Home Assistant URL: %s\n", haURL)
	fmt.Fprintln(writer)

	fmt.Fprintln(writer, "SUMMARY")
	fmt.Fprintln(writer, strings.Repeat("-", 30))
	fmt.Fprintf(writer, "Total Live Entities: %d\n", analysis.TotalLiveEntities)
	fmt.Fprintf(writer, "Total Config References: %d\n", analysis.TotalConfigRefs)
	fmt.Fprintf(writer, "Exact Matches: %d\n", analysis.ExactMatches)
	fmt.Fprintf(writer, "Missing/Mismatched: %d\n", len(analysis.MissingEntities))
	if analysis.TotalConfigRefs > 0 {
		matchRate := float64(analysis.ExactMatches) / float64(analysis.TotalConfigRefs) * 100
		fmt.Fprintf(writer, "Match Rate: %.1f%%\n", matchRate)
	}
	fmt.Fprintln(writer)

	if len(analysis.Mismatches) > 0 {
		fmt.Fprintln(writer, "ENTITY MISMATCHES")
		fmt.Fprintln(writer, strings.Repeat("-", 30))

		for _, m := range analysis.Mismatches {
			confidenceIcon := "!"
			if m.Confidence > 0.9 {
				confidenceIcon = "***"
			} else if m.Confidence > 0.7 {
				confidenceIcon = "**"
			}

			fmt.Fprintf(writer, "%s %s\n", confidenceIcon, m.ConfigEntity)
			fmt.Fprintf(writer, "   Suggested: %s\n", m.SuggestedEntity)
			fmt.Fprintf(writer, "   Type: %s\n", m.MismatchType)
			fmt.Fprintf(writer, "   Confidence: %.1f%%\n", m.Confidence*100)
			if m.FilePath != "" {
				fmt.Fprintf(writer, "   Location: %s:%d\n", m.FilePath, m.LineNumber)
			}
			fmt.Fprintln(writer)
		}
	}

	missingNoSuggestion := getMissingWithoutSuggestions(analysis)
	if len(missingNoSuggestion) > 0 {
		fmt.Fprintln(writer, "ENTITIES NOT FOUND (No Suggestions)")
		fmt.Fprintln(writer, strings.Repeat("-", 30))
		for _, entity := range missingNoSuggestion {
			fmt.Fprintf(writer, "? %s\n", entity)
		}
		fmt.Fprintln(writer)
	}

	highConfFixes := getHighConfidenceFixes(analysis)
	if len(highConfFixes) > 0 {
		fmt.Fprintln(writer, "RECOMMENDED FIXES (>90% confidence)")
		fmt.Fprintln(writer, strings.Repeat("-", 30))
		for old, new := range highConfFixes {
			fmt.Fprintf(writer, "  %s -> %s\n", old, new)
		}
		fmt.Fprintln(writer)
	}

	if err := util.WriteFileAtomic(reportPath, writer.Bytes(), 0644); err != nil {
		return nil, fmt.Errorf("failed to write report: %w", err)
	}

	return &MismatchReport{
		Analysis:   analysis,
		ReportPath: reportPath,
	}, nil
}

// PrintMismatchSummary prints a summary of mismatches to console
func PrintMismatchSummary(analysis *MismatchAnalysis) {
	fmt.Println()
	color.New(color.FgCyan, color.Bold).Println("Mismatch Analysis Summary")
	fmt.Println(strings.Repeat("-", 40))
	fmt.Printf("Live Entities: %d\n", analysis.TotalLiveEntities)
	fmt.Printf("Config References: %d\n", analysis.TotalConfigRefs)
	fmt.Printf("Exact Matches: %d\n", analysis.ExactMatches)
	fmt.Printf("Missing/Mismatched: %d\n", len(analysis.MissingEntities))

	if analysis.TotalConfigRefs > 0 {
		matchRate := float64(analysis.ExactMatches) / float64(analysis.TotalConfigRefs) * 100
		if matchRate >= 95 {
			color.Green("Match Rate: %.1f%%", matchRate)
		} else if matchRate >= 80 {
			color.Yellow("Match Rate: %.1f%%", matchRate)
		} else {
			color.Red("Match Rate: %.1f%%", matchRate)
		}
	}

	highConfCount := 0
	for _, m := range analysis.Mismatches {
		if m.Confidence > 0.9 {
			highConfCount++
		}
	}
	if highConfCount > 0 {
		color.Cyan("\nHigh Confidence Fixes Available: %d", highConfCount)
	}

	if len(analysis.MissingEntities) > 0 {
		fmt.Println()
		color.Yellow("Missing entities (top 5):")
		limit := 5
		if len(analysis.MissingEntities) < limit {
			limit = len(analysis.MissingEntities)
		}
		for i := 0; i < limit; i++ {
			entity := analysis.MissingEntities[i]
			found := false
			for _, m := range analysis.Mismatches {
				if m.ConfigEntity == entity {
					fmt.Printf("  %s -> %s (%.0f%%)\n", entity, m.SuggestedEntity, m.Confidence*100)
					found = true
					break
				}
			}
			if !found {
				fmt.Printf("  %s (no suggestion)\n", entity)
			}
		}
		if len(analysis.MissingEntities) > 5 {
			fmt.Printf("  ... and %d more\n", len(analysis.MissingEntities)-5)
		}
	}
}

type entityLocation struct {
	file string
	line int
}

func getYAMLFilesForMismatch(path string) []string {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}

	if !info.IsDir() {
		if util.IsYAMLFile(path) && !strings.Contains(filepath.Base(path), "secrets") {
			return []string{path}
		}
		return nil
	}

	var files []string
	filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if util.IsYAMLFile(p) && !strings.Contains(filepath.Base(p), "secrets") {
			files = append(files, p)
		}
		return nil
	})

	return files
}

func extractConfigEntities(path, configPath string) (map[string][]entityLocation, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	refs, err := hayaml.ExtractReferences(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	entities := map[string][]entityLocation{}
	for _, ref := range refs.Static {
		entities[ref.Entity] = append(entities[ref.Entity], entityLocation{file: util.RelPath(configPath, path), line: ref.Line})
	}
	return entities, nil
}

func findBestMatch(target string, liveEntities map[string]bool) (string, float64) {
	parts := strings.SplitN(target, ".", 2)
	if len(parts) != 2 {
		return "", 0
	}
	targetDomain := parts[0]
	targetName := parts[1]

	bestMatch := ""
	bestScore := 0.0

	for entity := range liveEntities {
		parts := strings.SplitN(entity, ".", 2)
		if len(parts) != 2 || parts[0] != targetDomain {
			continue
		}
		liveName := parts[1]

		score := calculateSimilarity(targetName, liveName)

		if score > bestScore && score > 0.5 {
			bestScore = score
			bestMatch = entity
		}
	}

	return bestMatch, bestScore
}

func calculateSimilarity(a, b string) float64 {
	if a == b {
		return 1.0
	}

	commonReplacements := []struct {
		old, new string
	}{
		{"_lights", "_light"},
		{"_light", "_lights"},
		{"filter_life_level", "filter_lifetime"},
		{"landscaping", "landscape_lights"},
		{"exterior_day", "daytime_exterior"},
		{"exterior_night", "night_exterior_lights"},
		{"interior_night", "interior_lighting_night"},
	}

	for _, r := range commonReplacements {
		if strings.Replace(a, r.old, r.new, 1) == b {
			return 0.95
		}
	}

	// Word overlap
	aWords := strings.FieldsFunc(a, func(c rune) bool { return c == '_' })
	bWords := strings.FieldsFunc(b, func(c rune) bool { return c == '_' })

	overlap := 0
	for _, aw := range aWords {
		for _, bw := range bWords {
			if aw == bw {
				overlap++
				break
			}
		}
	}

	unionSize := len(aWords) + len(bWords) - overlap
	if unionSize > 0 {
		wordScore := float64(overlap) / float64(unionSize)
		if wordScore > 0.5 {
			return wordScore
		}
	}

	return util.Similarity(a, b)
}

func getMissingWithoutSuggestions(analysis *MismatchAnalysis) []string {
	suggested := make(map[string]bool)
	for _, m := range analysis.Mismatches {
		suggested[m.ConfigEntity] = true
	}

	var missing []string
	for _, entity := range analysis.MissingEntities {
		if !suggested[entity] {
			missing = append(missing, entity)
		}
	}
	return missing
}

func getHighConfidenceFixes(analysis *MismatchAnalysis) map[string]string {
	fixes := make(map[string]string)
	for _, m := range analysis.Mismatches {
		if m.Confidence > 0.9 {
			fixes[m.ConfigEntity] = m.SuggestedEntity
		}
	}
	return fixes
}
