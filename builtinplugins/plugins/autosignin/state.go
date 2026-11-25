package autosignin

import "sync"

// Source fields are shared by overlapping callbacks. Each source expression
// reads the current member; no whole-job config snapshot or mutex is held over
// host/network operations. A frame's data and dependency hooks remain local.
type sourceState struct {
	mu     sync.RWMutex
	values Object
}

func (e *Engine) field(name string) any {
	if e.shared != nil {
		e.shared.mu.RLock()
		defer e.shared.mu.RUnlock()
		return e.shared.values[name]
	}
	switch name {
	case "Enabled":
		return e.Enabled
	case "Notify":
		return e.Notify
	case "Cron":
		return e.Cron
	case "OnlyOnce":
		return e.OnlyOnce
	case "QueueCount":
		return e.QueueCount
	case "SignSites":
		return e.SignSites
	case "LoginSites":
		return e.LoginSites
	case "RetryKeyword":
		return e.RetryKeyword
	case "AutoCF":
		return e.AutoCF
	case "Clean":
		return e.Clean
	case "StartTime":
		return e.StartTime
	case "EndTime":
		return e.EndTime
	case "SchemaLoaded":
		return e.SchemaLoaded
	}
	panic("unknown source member")
}
func (e *Engine) setField(name string, v any) {
	if e.shared != nil {
		e.shared.mu.Lock()
		e.shared.values[name] = v
		e.shared.mu.Unlock()
		return
	}
	switch name {
	case "Enabled":
		e.Enabled = v
	case "Notify":
		e.Notify = v
	case "Cron":
		e.Cron = v
	case "OnlyOnce":
		e.OnlyOnce = v
	case "QueueCount":
		e.QueueCount = v
	case "SignSites":
		e.SignSites = v
	case "LoginSites":
		e.LoginSites = v
	case "RetryKeyword":
		e.RetryKeyword = v
	case "AutoCF":
		e.AutoCF = v
	case "Clean":
		e.Clean = v
	case "StartTime":
		e.StartTime = v
	case "EndTime":
		e.EndTime = v
	case "SchemaLoaded":
		e.SchemaLoaded = Truth(v)
	default:
		panic("unknown source member")
	}
}
func (e *Engine) schemaLoaded() bool { return Truth(e.field("SchemaLoaded")) }
func (e *Engine) configured() []Object {
	if e.ConfiguredLive != nil {
		return e.ConfiguredLive()
	}
	return e.Inventory.Configured
}
func (e *Engine) indexers() []Object {
	if e.IndexersLive != nil {
		return e.IndexersLive()
	}
	return e.Inventory.Indexers
}
