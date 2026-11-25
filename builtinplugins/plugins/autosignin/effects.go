package autosignin

import "errors"

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
