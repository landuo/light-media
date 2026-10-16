package cleaninvalidseed

import "context"

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
