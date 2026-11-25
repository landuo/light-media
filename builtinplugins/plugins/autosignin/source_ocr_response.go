package autosignin

// Decode only original OCR HTTP bytes. Invalid JSON and a non-object .get are
// ordinary source exceptions; they must not become a host capability failure.
func sourceOCRServiceResult(content []byte, contentType string) (any, error) {
	text, err := responseText(content, contentType)
	if err != nil {
		return nil, err
	}
	value, err := parseSiteJSON(text)
	if err != nil {
		return nil, err
	}
	object, err := jsonObject(value)
	if err != nil {
		return nil, err
	}
	result := object["result"]
	if !Truth(result) {
		return "", nil
	}
	// Keep the already-decoded source HTTP value. It may legally include
	// nonfinite floats, ordered maps, huge integers or lone codepoints.
	return &sourceOCRValue{value: result, fromHTTP: true}, nil
}
