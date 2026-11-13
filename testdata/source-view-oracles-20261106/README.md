# Source view independent oracle prerequisites

The numeric-success and serialization directories preserve original synthetic
cases.json and source.stdout bytes from the prior 2026-11-06 source-first
verification. Original per-case HTTP response bodies are base64, not re-generated
Go expectations. Associated reports describe the precise original AST/FastAPI/
Pydantic scope and limits; no source production account data is included.
SHA-256 provenance is retained in evidence-origins.json.

Build the existing ./cmd/owned-source-view-fixture and bind its executable as
SOURCE_VIEW_FIXTURE_BINARY. Set SOURCE_NUMERIC_EVIDENCE_DIR to numeric-success and
SOURCE_VIEW_EVIDENCE_DIR to serialization. These must be paths relative to the
checkout or explicit caller inputs, never hardcoded developer-machine paths.

`go test -p=1 ./internal/app -run '^(TestSourceViewNumericSuccessActualSDKHTTP|TestSourceViewSerializationActualSDKHTTP)$' -count=1`

The source matrices remain immutable. Tests compare successful semantic numeric
values and response phases with the recorded source results; missing environment
or evidence must be a preflight failure, not a skip.
