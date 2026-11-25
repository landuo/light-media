package removelink

import (
	"encoding/json"
	"sync"
)

// One source invocation owns its effects and failure fence. Only the source
// state/queue/config are shared; a concurrent observer never borrows a timer's
// live caller. There is no persisted operation or caller registry.
type operation struct {
	*Machine
	Ops         Ops
	noticeMu    sync.Mutex
	noticeFatal error
}

func (m *Machine) Init(raw json.RawMessage) error { return m.defaultOp.Init(raw) }
func (m *Machine) Stop()                          { m.defaultOp.Stop() }
func (m *Machine) Tick()                          { m.defaultOp.Tick() }
func (m *Machine) Created(path string, directory bool) error {
	return m.defaultOp.Created(path, directory)
}
func (m *Machine) Moved(path, dest, mode string, directory bool) error {
	return m.defaultOp.Moved(path, dest, mode, directory)
}
func (m *Machine) Deleted(path, mode string, directory bool) error {
	return m.defaultOp.Deleted(path, mode, directory)
}
func (m *operation) noticeFailure() error {
	m.noticeMu.Lock()
	defer m.noticeMu.Unlock()
	return m.noticeFatal
}
func (m *operation) failNotice(err error) {
	m.noticeMu.Lock()
	m.noticeFatal = err
	m.noticeMu.Unlock()
}
func (m *Machine) recordError(err error, source bool) {
	m.errorsMu.Lock()
	defer m.errorsMu.Unlock()
	if source {
		m.SourceErrors = append(m.SourceErrors, err)
	} else {
		m.Errors = append(m.Errors, err)
	}
}
func (m *Machine) wakeTimer() {
	if m.timerWake != nil {
		m.timerWake()
	}
}
func (m *Machine) markProcessed(task *Task) {
	m.queueMu.Lock()
	task.Processed = true
	m.queueMu.Unlock()
}
