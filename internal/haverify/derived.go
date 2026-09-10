package haverify

// NormalizeDerivedExpectations copies expectations without changing the configured
// preset. Explicit mmWave ranges can match a named preset, so they do not imply
// that mmWaveRoomSizePreset should read back as Custom.
func NormalizeDerivedExpectations(expectations []ParamExpectation) []ParamExpectation {
	return append([]ParamExpectation(nil), expectations...)
}
