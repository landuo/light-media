package app

import (
	"bytes"
	"encoding/json"
	"light-media/internal/plugins/schedule"
	"time"
)

// APS only consumes constructor kwargs when the original trigger is None.
// Constructed builtin objects bypass this function and keep ignoring extras.
func originalBuiltinDefaultDate(raw json.RawMessage, schedulerZone string) (*schedule.BuiltinTrigger, error) {
	location, err := time.LoadLocation(schedulerZone)
	if err != nil {
		return nil, err
	}
	return originalBuiltinDefaultDateResolved(raw, location, func() *time.Location { return time.Local })
}

// Explicit parameter-only seam: production above always uses actual time.Local.
// Source-differential tests may supply the original recording resolver result;
// this does not prove tzlocal discovery or install any App/global replacement.
func originalBuiltinDefaultDateResolved(raw json.RawMessage, location *time.Location, local func() *time.Location) (*schedule.BuiltinTrigger, error) {
	if location == nil || local == nil {
		return nil, &schedule.BuiltinBoundaryError{Code: "invalid_date_constructor_port"}
	}
	fields := map[string]json.RawMessage{}
	if originalViewJSONTruth(raw) {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		token, err := decoder.Token()
		if err != nil || token != json.Delim('{') {
			return nil, bad("INVALID_PLUGIN_SERVICE_OPTIONS", "服务时钟参数必须为对象")
		}
		for decoder.More() {
			token, err = decoder.Token()
			if err != nil {
				return nil, err
			}
			name, ok := token.(string)
			if !ok {
				return nil, bad("INVALID_PLUGIN_SERVICE_OPTIONS", "服务时钟参数必须为对象")
			}
			var value json.RawMessage
			if err = decoder.Decode(&value); err != nil {
				return nil, err
			}
			switch name {
			case "run_date", "timezone":
				fields[name] = append(json.RawMessage(nil), value...)
			case "args", "kwargs", "id", "name", "func", "trigger", "misfire_grace_time", "coalesce", "max_instances", "next_run_time", "jobstore", "executor", "replace_existing":
				// Existing ordinary job option/duplicate-key validation remains separate.
			default:
				return schedule.NewBuiltinDateFromArguments(schedule.BuiltinDateArguments{UnexpectedKeyword: &name}, location, local, originalBuiltinClockLimits())
			}
		}
	}
	args := schedule.BuiltinDateArguments{}
	if rawZone, exists := fields["timezone"]; exists {
		args.TimezonePresent = true
		if !bytes.Equal(bytes.TrimSpace(rawZone), []byte("null")) {
			var zone string
			if json.Unmarshal(rawZone, &zone) != nil {
				return nil, &schedule.BuiltinBoundaryError{Code: "date_timezone_scalar"}
			}
			args.Timezone = &zone
		}
	}
	if rawDate, exists := fields["run_date"]; exists && !bytes.Equal(bytes.TrimSpace(rawDate), []byte("null")) {
		var text string
		if json.Unmarshal(rawDate, &text) != nil {
			return nil, &schedule.BuiltinBoundaryError{Code: "date_run_date_scalar"}
		}
		args.RunDate = &text
	}
	return schedule.NewBuiltinDateFromArguments(args, location, local, originalBuiltinClockLimits())
}
