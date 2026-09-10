package util

import (
	"testing"
)

func TestLevenshteinDistance(t *testing.T) {
	tests := []struct {
		a, b     string
		expected int
	}{
		{"hello", "hello", 0},
		{"", "", 0},
		{"a", "a", 0},

		{"hello", "", 5},
		{"", "hello", 5},

		{"hello", "hallo", 1},
		{"hello", "jello", 1},
		{"cat", "hat", 1},

		{"kitten", "sitting", 3},
		{"saturday", "sunday", 3},
		{"book", "back", 2},

		{"abc", "xyz", 3},
		{"hello", "world", 4},

		{"hello", "helloo", 1},
		{"hello", "helo", 1},
		{"test", "testing", 3},

		{"Hello", "hello", 1},
		{"ABC", "abc", 3},

		{"light.office_overhead", "light.office_overhad", 1},
		{"light.office_overhead", "light.office_overhead_north", 6},
		{"binary_sensor.garage", "binary_sensor.garaje", 1},
	}

	for _, tt := range tests {
		t.Run(tt.a+"_vs_"+tt.b, func(t *testing.T) {
			got := LevenshteinDistance(tt.a, tt.b)
			if got != tt.expected {
				t.Errorf("LevenshteinDistance(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.expected)
			}

			gotReverse := LevenshteinDistance(tt.b, tt.a)
			if gotReverse != tt.expected {
				t.Errorf("LevenshteinDistance(%q, %q) = %d, want %d (symmetry test)", tt.b, tt.a, gotReverse, tt.expected)
			}
		})
	}
}

func TestSimilarity(t *testing.T) {
	tests := []struct {
		a, b     string
		minScore float64
		maxScore float64
	}{
		{"hello", "hello", 0.99, 1.0},
		{"light.office", "light.office", 0.99, 1.0},

		{"", "", 0.99, 1.0},

		{"hello", "", 0.0, 0.01},
		{"", "hello", 0.0, 0.01},

		{"light.office_overhead", "light.office_overhad", 0.9, 1.0},
		{"binary_sensor.garage", "binary_sensor.garaje", 0.9, 1.0},

		{"light.office", "light.garage", 0.5, 0.8},
		{"sensor.temp", "sensor.humidity", 0.3, 0.6},

		{"light.office", "switch.garage", 0.2, 0.5},
		{"abc", "xyz", 0.0, 0.01},
	}

	for _, tt := range tests {
		t.Run(tt.a+"_vs_"+tt.b, func(t *testing.T) {
			got := Similarity(tt.a, tt.b)
			if got < tt.minScore || got > tt.maxScore {
				t.Errorf("Similarity(%q, %q) = %v, want between %v and %v",
					tt.a, tt.b, got, tt.minScore, tt.maxScore)
			}

			gotReverse := Similarity(tt.b, tt.a)
			if gotReverse != got {
				t.Errorf("Similarity is not symmetric: Similarity(%q, %q) = %v, Similarity(%q, %q) = %v",
					tt.a, tt.b, got, tt.b, tt.a, gotReverse)
			}
		})
	}
}

func TestSimilarityRange(t *testing.T) {
	testPairs := [][2]string{
		{"hello", "world"},
		{"abc", "abcdef"},
		{"", "test"},
		{"similar", "smilar"},
		{"completely", "different"},
	}

	for _, pair := range testPairs {
		score := Similarity(pair[0], pair[1])
		if score < 0 || score > 1 {
			t.Errorf("Similarity(%q, %q) = %v, should be between 0 and 1",
				pair[0], pair[1], score)
		}
	}
}

func BenchmarkLevenshteinDistance(b *testing.B) {
	a := "light.office_overhead_north"
	c := "light.office_overhad_north"

	for i := 0; i < b.N; i++ {
		LevenshteinDistance(a, c)
	}
}

func BenchmarkSimilarity(b *testing.B) {
	a := "light.office_overhead_north"
	c := "light.office_overhad_north"

	for i := 0; i < b.N; i++ {
		Similarity(a, c)
	}
}

func BenchmarkLevenshteinLongStrings(b *testing.B) {
	a := "binary_sensor.living_room_ceiling_fan_occupancy_sensor_north"
	c := "binary_sensor.living_room_ceiling_fan_occupancy_sensor_south"

	for i := 0; i < b.N; i++ {
		LevenshteinDistance(a, c)
	}
}
