package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"light-media/internal/plugins/schedule"
)

func decodeOriginalBuiltinServices(raw json.RawMessage) ([]pluginScheduleDefinition, error) {
	var defs []pluginScheduleDefinition
	if err := json.Unmarshal(raw, &defs); err != nil {
		return nil, err
	}
	var fields []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	for i, field := range fields {
		defs[i].builtinDecoded = true
		trigger, exists := field["trigger"]
		if !exists {
			defs[i].triggerMissing = true
			continue
		}
		if bytes.Equal(bytes.TrimSpace(trigger), []byte("null")) {
			continue
		}
		// Preserve existing ordinary top-level JSON unknown-field handling. New
		// ordered children retain their finite typed descriptor decoder.
		type plain schedule.BuiltinDescriptor
		var descriptor plain
		if err := json.Unmarshal(trigger, &descriptor); err != nil {
			return nil, err
		}
		d := schedule.BuiltinDescriptor(descriptor)
		defs[i].builtinDescriptor = &d
	}
	return defs, nil
}

func originalServiceBuiltinDescriptor(definition pluginScheduleDefinition) (*schedule.BuiltinDescriptor, error) {
	if definition.builtinDecoded {
		if definition.triggerMissing {
			return nil, &schedule.BuiltinSourceError{Class: "KeyError", Message: "'trigger'"}
		}
		return definition.builtinDescriptor, nil
	}
	// Direct typed Go registrations preserve their constructed Spec. Missing
	// source service keys are detected only from real get_service wire presence.
	s := definition.Trigger
	d := &schedule.BuiltinDescriptor{Kind: s.Kind, Timezone: s.Timezone, Cron: s.Cron, Fields: s.Fields, IntervalMS: s.IntervalMS, RunAt: s.RunAt, StartAt: s.StartAt, EndAt: s.EndAt, Calendar: s.Calendar, Children: s.Children}
	if s.JitterSeconds != 0 {
		j := s.JitterSeconds
		d.Jitter = &j
	}
	return d, nil
}

func originalBuiltinVisibleFault(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	switch err.(type) {
	case *schedule.BuiltinBoundaryError, *schedule.CalendarIntervalBoundaryError, *schedule.CalendarIntervalSourceError:
		return true
	case *schedule.BuiltinSourceError:
		// Missing trigger/Date constructor arguments are registration failures,
		// not a calculated clock. Their methods still remain registered.
		return err.(*schedule.BuiltinSourceError).Class != "KeyError" && err.(*schedule.BuiltinSourceError).Class != "TypeError"
	}
	return false
}
