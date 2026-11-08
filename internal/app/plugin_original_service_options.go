package app

import (
	"bytes"
	"encoding/json"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"time"
)

type originalResolvedServiceOptions struct {
	coalesceDisabled, unlimited bool
	hasNextRun                  bool
	nextRunPresent              bool
	nextRun                     time.Time
	executorUnavailable         bool
	maxInstances                *big.Int
}

// Native trigger/input values represent constructed trigger/func_kwargs values.
// kwargs retains ordinary APScheduler job options; it never authorizes an
// arbitrary method, durable executor or workflow extension.
func originalServiceOptions(definition *pluginScheduleDefinition) (options originalResolvedServiceOptions, err error) {
	if definition.MisfireGraceMS < 0 {
		return options, bad("INVALID_PLUGIN_SERVICE_OPTIONS", "失火宽限必须为非负毫秒")
	}
	if definition.MisfireGraceMS == 0 {
		definition.MisfireGraceMS = 1000
	}
	if !originalViewJSONTruth(definition.SchedulerOptions) {
		return options, nil
	}
	var input map[string]json.RawMessage
	if json.Unmarshal(definition.SchedulerOptions, &input) != nil || input == nil {
		return options, bad("INVALID_PLUGIN_SERVICE_OPTIONS", "服务时钟参数必须为对象")
	}
	invalid := func() (originalResolvedServiceOptions, error) {
		return options, bad("INVALID_PLUGIN_SERVICE_OPTIONS", "服务时钟参数无效")
	}
	for name, value := range input {
		switch name {
		case "coalesce":
			options.coalesceDisabled = !originalViewJSONTruth(value)
		case "misfire_grace_time":
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				options.unlimited = true
				continue
			}
			seconds, valid := originalServicePositiveInteger(value)
			if !valid || seconds > int64(math.MaxInt64)/int64(time.Second) {
				return invalid()
			}
			definition.MisfireGraceMS = seconds * 1000
		case "max_instances":
			// Scheduler.start itself admits one running method regardless of the
			// executor limit. Keep that guard while validating the source setting.
			if !originalServicePositiveIntegerValue(value) {
				return invalid()
			}
			if bytes.Equal(bytes.TrimSpace(value), []byte("true")) {
				options.maxInstances = big.NewInt(1)
			} else {
				options.maxInstances, _ = new(big.Int).SetString(string(bytes.TrimSpace(value)), 10)
			}
		case "executor":
			var alias string
			if json.Unmarshal(value, &alias) != nil {
				return invalid()
			}
			// APScheduler accepts an unknown alias at registration and drops only the
			// clock when it tries to submit; manual Scheduler.start still calls func.
			options.executorUnavailable = alias != "default"
		case "jobstore":
			var alias string
			if json.Unmarshal(value, &alias) != nil || alias != "default" {
				return invalid()
			}
		case "next_run_time":
			options.hasNextRun = true
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				continue
			}
			location, locationErr := definition.Trigger.Location()
			if locationErr != nil {
				return invalid()
			}
			options.nextRunPresent = true
			options.nextRun, err = originalServiceNextRunTime(value, location)
			if err != nil {
				return invalid()
			}
		case "args":
			var args any
			if json.Unmarshal(value, &args) != nil {
				return invalid()
			}
			switch v := args.(type) {
			case nil:
			case []any:
				if len(v) != 0 {
					return invalid()
				}
			case map[string]any:
				if len(v) != 0 {
					return invalid()
				}
			case string:
				if v != "" {
					return invalid()
				}
			default:
				return invalid()
			}
		case "id", "name", "func", "trigger", "kwargs", "replace_existing":
			return invalid() // source duplicate named argument
		default:
			// APScheduler ignores trigger_args when passed a constructed trigger. The
			// normalized native trigger has already resolved its own calendar fields.
		}
	}
	return options, nil
}

func originalServicePositiveInteger(raw json.RawMessage) (int64, bool) {
	raw = bytes.TrimSpace(raw)
	if bytes.Equal(raw, []byte("true")) {
		return 1, true
	}
	if len(raw) == 0 || raw[0] < '0' || raw[0] > '9' || bytes.ContainsAny(raw, ".eE") {
		return 0, false
	}
	value, err := strconv.ParseInt(string(raw), 10, 64)
	return value, err == nil && value > 0
}

var originalServiceDatePattern = regexp.MustCompile(`^(\d{4})-(\d{1,2})-(\d{1,2})(?:[ T](\d{1,2}):(\d{1,2}):(\d{1,2})(?:\.(\d{1,6}))?(Z|[+-]\d\d:\d\d)?)?$`)

func originalServiceNextRunTime(raw json.RawMessage, location *time.Location) (time.Time, error) {
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return time.Time{}, bad("INVALID_PLUGIN_SERVICE_OPTIONS", "下次运行时间无效")
	}
	// Python regexp '$' also permits one terminating newline.
	parts := originalServiceDatePattern.FindStringSubmatch(string(bytes.TrimSuffix([]byte(text), []byte("\n"))))
	if parts == nil {
		return time.Time{}, bad("INVALID_PLUGIN_SERVICE_OPTIONS", "下次运行时间格式无效")
	}
	n := func(i int) int { value, _ := strconv.Atoi(parts[i]); return value }
	if parts[8] == "Z" {
		location = time.UTC
	} else if parts[8] != "" {
		hours, _ := strconv.Atoi(parts[8][1:3])
		minutes, _ := strconv.Atoi(parts[8][4:])
		offset := hours*3600 + minutes*60
		if parts[8][0] == '-' {
			offset = -offset
		}
		if offset <= -86400 || offset >= 86400 {
			return time.Time{}, bad("INVALID_PLUGIN_SERVICE_OPTIONS", "时间偏移无效")
		}
		location = time.FixedZone("", offset)
	}
	value := time.Date(n(1), time.Month(n(2)), n(3), n(4), n(5), n(6), n(7)*1000, location)
	if n(1) < 1 || value.Year() != n(1) || int(value.Month()) != n(2) || value.Day() != n(3) || value.Hour() != n(4) || value.Minute() != n(5) || value.Second() != n(6) {
		return time.Time{}, bad("INVALID_PLUGIN_SERVICE_OPTIONS", "日期或时间无效")
	}
	return value, nil
}

func originalServicePositiveIntegerValue(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if bytes.Equal(raw, []byte("true")) {
		return true
	}
	if len(raw) == 0 || raw[0] < '0' || raw[0] > '9' || bytes.ContainsAny(raw, ".eE") {
		return false
	}
	value, ok := new(big.Int).SetString(string(raw), 10)
	return ok && value.Sign() > 0
}
