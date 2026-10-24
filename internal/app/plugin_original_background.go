package app

import "sync"

// Admission only owns process lifetime. It is not a durable job, receipt,
// revision check or plugin business enablement gate. Holding this mutex across
// Add and sealing it before Close waits prevents late external producers from
// adding callbacks to a drained WaitGroup.
type originalBackgroundAdmission struct {
	mu     sync.Mutex
	closed bool
}

func (a *App) admitOriginalBackground() bool {
	state := &a.pluginOriginalBackground
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed {
		return false
	}
	a.wg.Add(1)
	return true
}

func (a *App) closeOriginalBackgroundAdmission() {
	state := &a.pluginOriginalBackground
	state.mu.Lock()
	state.closed = true
	state.mu.Unlock()
}
