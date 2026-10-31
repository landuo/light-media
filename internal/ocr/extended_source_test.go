package ocr

import "testing"

// These fresh observations execute only the pinned OcrHelper AST with explicit
// deterministic responses and stdlib JSON; they are not requests/HTTP evidence.
func TestOrdinaryOCRExtendedSourceCodecAndValues(t *testing.T) {
	runSourceCases(t, "extended-http-source.json")
}
