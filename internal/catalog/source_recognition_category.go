package catalog

import (
	"bytes"
	"encoding/json"
	"io"
)

// SourceRecognitionCategoryField reuses only the existing source category parser,
// not the UI Media decoder or its unrelated identity/cast/title validation.
func (c *Client) SourceRecognitionCategoryField(raw json.RawMessage) (CategoryField, error) {
	// Classification is a pure source operation: do not apply the UI
	// credential redactor to a legitimate field before matching.
	return (&Client{}).categoryField(raw)
}
func SourceRecognitionCategoryText(raw json.RawMessage) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := categoryPythonValue(decoder, 0)
	if err != nil {
		return "", err
	}
	if _, err = decoder.Token(); err != io.EOF {
		return "", &Error{Code: "invalid_response"}
	}
	return value.text, nil
}
