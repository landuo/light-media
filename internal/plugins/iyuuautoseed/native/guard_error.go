package download

// Preserve the trusted owner's exact rejection through http.Client's URL error
// and the native client's sanitization. The text remains the preexisting public
// transport error; neither its cause nor request URL is rendered into logs.
type iyuuGuardError struct{ cause error }

func (e *iyuuGuardError) Error() string {
	return "downloader request: transport_failed_outcome_unknown"
}
func (e *iyuuGuardError) Unwrap() error { return e.cause }
