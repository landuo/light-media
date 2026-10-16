package autosignin

import (
	"encoding/json"
	"errors"
)

type SourceFailure struct{ Message, Class string }

func (e *SourceFailure) Error() string { return e.Message }
func sourceFailure(err error) bool     { var failure *SourceFailure; return errors.As(err, &failure) }

type Effect struct {
	Kind, Key string
	Value     any
	Runtime   Object
}

func (e *Engine) effect(kind, key string, value any) error {
	if e.Effects == nil {
		return nil
	}
	return e.Effects(Effect{kind, key, clone(value), e.Runtime()})
}
func (e *Engine) Runtime() Object {
	out := e.Snapshot()
	delete(out, "data")
	delete(out, "trace")
	if e.Scheduler != nil {
		out["scheduler"] = Object{"running": e.Scheduler.Running}
	}
	return clone(out).(Object)
}

// Restore is host-only. A source cold start/reload uses New+Init instead;
// same-runtime short workers restore every instance field, including windows.
func (e *Engine) Restore(runtime Object, data Object) error {
	raw, err := json.Marshal(runtime)
	if err != nil {
		return err
	}
	var c Object
	if err = Decode(raw, &c); err != nil {
		return err
	}
	e.Enabled, e.Notify, e.Cron, e.OnlyOnce = c["enabled"], c["notify"], c["cron"], c["onlyonce"]
	e.QueueCount, e.SignSites, e.LoginSites = c["queue_cnt"], c["sign_sites"], c["login_sites"]
	e.RetryKeyword, e.AutoCF, e.Clean = c["retry_keyword"], c["auto_cf"], c["clean"]
	e.StartTime, e.EndTime = c["start_time"], c["end_time"]
	e.SchemaLoaded = Truth(c["schema_loaded"])
	e.Config = asObject(clone(c["config"]))
	e.Scheduler = nil
	if s := asObject(c["scheduler"]); s != nil {
		e.Scheduler = &scheduler{Running: Truth(s["running"])}
	}
	e.Data = asObject(clone(data))
	if e.Data == nil {
		e.Data = Object{}
	}
	e.Trace = []Object{}
	return nil
}
