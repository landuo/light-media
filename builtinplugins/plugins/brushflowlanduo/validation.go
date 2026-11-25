package brushflowlanduo

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
)

type validationDetail struct {
	Type  string   `json:"type"`
	Loc   []string `json:"loc"`
	Msg   string   `json:"msg"`
	Input any      `json:"input"`
	Ctx   Row      `json:"ctx,omitempty"`
}

type ValidationError struct {
	scope              string
	details            []validationDetail
	uncaughtRegexError bool
}

func (v *ValidationError) Error() string {
	return strings.Join(v.details[0].Loc, ".") + ": " + v.details[0].Msg
}

func (v *ValidationError) add(field, code, message string, input any, params Row) {
	loc := []string{v.scope}
	if field != "" {
		loc = append(loc, field)
	}
	v.details = append(v.details, validationDetail{code, loc, message, input, params})
}

func (v *ValidationError) value(field, message string, input any) {
	v.add(field, "value_error", "Value error, "+message, input, Row{"error": Row{}})
}

func (v *ValidationError) response() (json.RawMessage, error) {
	return standardResponse(422, Row{"detail": v.details})
}

func (v *ValidationError) boolean(field string, input any) bool {
	b, ok := pydanticBool(input)
	if !ok {
		switch n := input.(type) {
		case string:
			v.add(field, "bool_parsing", "Input should be a valid boolean, unable to interpret input", input, nil)
			return false
		case float64, int, int64, json.Number:
			f := number(n)
			if math.Trunc(f) == f {
				v.add(field, "bool_parsing", "Input should be a valid boolean, unable to interpret input", input, nil)
				return false
			}
		}
		v.add(field, "bool_type", "Input should be a valid boolean", input, nil)
	}
	return b
}

var integerString = regexp.MustCompile(`^[+-]?[0-9](?:_?[0-9])*(?:\.0(?:_?0)*)?$`)

func (v *ValidationError) integer(field string, input any, min, max int) int {
	projection := input
	if s, ok := input.(string); ok {
		s = strings.TrimSpace(s)
		if integerString.MatchString(s) {
			projection = strings.ReplaceAll(s, "_", "")
		} else {
			v.add(field, "int_parsing", "Input should be a valid integer, unable to parse string as an integer", input, nil)
			return 0
		}
	}
	n, ok := parseSiteID(projection, true)
	switch {
	case !ok:
		switch input.(type) {
		case string:
			v.add(field, "int_parsing", "Input should be a valid integer, unable to parse string as an integer", input, nil)
		case json.Number, float64, float32:
			v.add(field, "int_from_float", "Input should be a valid integer, got a number with a fractional part", input, nil)
		default:
			v.add(field, "int_type", "Input should be a valid integer", input, nil)
		}
	case min == 0 && n <= 0:
		v.add(field, "greater_than", "Input should be greater than 0", input, Row{"gt": 0})
	case n < min:
		v.add(field, "greater_than_equal", fmt.Sprintf("Input should be greater than or equal to %d", min), input, Row{"ge": min})
	case max > 0 && n > max:
		v.add(field, "less_than_equal", fmt.Sprintf("Input should be less than or equal to %d", max), input, Row{"le": max})
	}
	return n
}

func (v *ValidationError) number(field string, input any) any {
	n := parseNumber(input)
	optional := field != "brush_interval" && field != "check_interval" && field != "timezone_offset"
	if optional && (input == nil || input == "" || n != nil && number(n) == 0) {
		return nil
	}
	switch field {
	case "brush_interval", "check_interval":
		return v.integer(field, input, 1, 1440)
	case "maxdlcount", "global_maxdlcount":
		return v.integer(field, input, 0, 0)
	}
	if n == nil {
		if _, ok := input.(string); ok {
			v.add(field, "float_parsing", "Input should be a valid number, unable to parse string as a number", input, nil)
		} else {
			v.add(field, "float_type", "Input should be a valid number", input, nil)
		}
		return nil
	}
	if field != "timezone_offset" && !(number(n) > 0) {
		v.add(field, "greater_than", "Input should be greater than 0", input, Row{"gt": 0})
	} else if math.IsNaN(number(n)) || math.IsInf(number(n), 0) {
		v.add(field, "finite_number", "Input should be a finite number", input, nil)
	}
	return n
}

func (v *ValidationError) requiredText(field string, input any) string {
	s, ok := input.(string)
	if !ok {
		v.add(field, "string_type", "Input should be a valid string", input, nil)
		return ""
	}
	switch length := len([]rune(s)); {
	case length < 1:
		v.add(field, "string_too_short", "String should have at least 1 character", input, Row{"min_length": 1})
	case length > 80:
		v.add(field, "string_too_long", "String should have at most 80 characters", input, Row{"max_length": 80})
	case strings.TrimSpace(s) == "":
		v.value(field, "字段不能为空", input)
	}
	return strings.TrimSpace(s)
}

func timeRangeError(value string) string {
	re := regexp.MustCompile(`^[\p{Nd}]{2}:[\p{Nd}]{2}-[\p{Nd}]{2}:[\p{Nd}]{2}$`)
	if !re.MatchString(value) {
		return "开启时间段格式应为 HH:MM-HH:MM"
	}
	for _, part := range strings.Split(value, "-") {
		if validTimeRange(part + "-" + part) {
			continue
		}
		// CPython %M can match the first digit of an out-of-range minute.
		if validTimeRange(string([]rune(part)[:2])+":00-00:00") && []rune(part)[3] >= '0' && []rune(part)[3] <= '9' {
			return "unconverted data remains: " + string([]rune(part)[4])
		}
		return "time data '" + part + "' does not match format '%H:%M'"
	}
	return ""
}
