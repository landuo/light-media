package cleaninvalidseed

import (
	"context"
	"regexp"
)

var diagnosticURL = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s<>"']+`)
var diagnosticCredential = regexp.MustCompile(`(?i)(?:password|passwd|pwd|cookie|authorization|api[_-]?key|access[_-]?token|refresh[_-]?token|token|passkey|secret|session[_-]?id)\s*["']?\s*[:=]\s*(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;}&]+)`)

func diagnosticText(value any) string {
	return diagnosticCredential.ReplaceAllString(diagnosticURL.ReplaceAllString(str(value), "[URL]"), "[CREDENTIAL]")
}

// LogHost routes branch progress to the host plugin-log scope. The kernel has
// no global logger, network destination or credentials. Logging is optional
// for isolated pure consumers; the live App adapter must implement this hook.
type LogHost interface {
	Log(context.Context, string, string)
}

func (m *Machine) log(ctx context.Context, level, text string) {
	if h, ok := m.Host.(LogHost); ok {
		h.Log(ctx, level, text)
	}
}
