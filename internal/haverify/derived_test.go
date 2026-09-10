package haverify

import "testing"

func TestNormalizeDerivedExpectationsPreservesPresetWithExplicitMmWaveRanges(t *testing.T) {
	expectations := NormalizeDerivedExpectations([]ParamExpectation{
		{MQTTParam: "mmWaveRoomSizePreset", Value: "Small"},
		{MQTTParam: "mmWaveHeightMin", Value: "-600"},
	})

	if expectations[0].Value != "Small" {
		t.Fatalf("expected room size preset to stay Small, got %q", expectations[0].Value)
	}
}

func TestNormalizeDerivedExpectationsLeavesPresetWithoutExplicitRanges(t *testing.T) {
	expectations := NormalizeDerivedExpectations([]ParamExpectation{
		{MQTTParam: "mmWaveRoomSizePreset", Value: "Small"},
		{MQTTParam: "mmWaveDetectTrigger", Value: "Medium (1s)"},
	})

	if expectations[0].Value != "Small" {
		t.Fatalf("expected room size preset to stay Small, got %q", expectations[0].Value)
	}
}
