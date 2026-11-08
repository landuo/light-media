package download

// This optional text is populated only by the standard SourceAdd host path.
// Ordinary bool clients keep their existing True/False field bytes.
func iyuuSkipCheckingField(spec IYUUAddSpec) (string, bool) {
	if spec.SkipCheckingWire != nil {
		return *spec.SkipCheckingWire, true
	}
	if spec.SkipChecking != nil {
		return iyuuBool(*spec.SkipChecking), true
	}
	return "", false
}
