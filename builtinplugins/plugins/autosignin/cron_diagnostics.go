package autosignin

import (
	"fmt"
	"math/big"
	"regexp"
	"strings"

	"light-media/builtinplugins/plugins/autosignin/pyvalue"
)

// This finite compile/diagnostic leaf follows the installed APScheduler3
// fields.py/expressions.py. Its constructor and validate_range errors have
// different wrapping; preserve the original field and original token text.
var cronAllDiagnostic = regexp.MustCompile(`^\*(?:/(` + sourceDecimalClass + `+))?$`)
var cronRangeDiagnostic = regexp.MustCompile(`^(` + sourceDecimalClass + `+)(?:-(` + sourceDecimalClass + `+))?(?:/(` + sourceDecimalClass + `+))?$`)
var cronNameDiagnostic = regexp.MustCompile(`(?i)^([a-zİıſK]+)(?:-([a-zİıſK]+))?`)
var cronLastDiagnostic = regexp.MustCompile(`(?i)^last`)

func sourceCronCompileError(expression string) error {
	parts := strings.FieldsFunc(expression, sourcePythonSpace)
	if len(parts) != 5 {
		return fmt.Errorf("Wrong number of fields; got %d, expected 5", len(parts))
	}
	// CronTrigger constructs in calendar significance order, not crontab order.
	for _, field := range []struct {
		name, value string
		min, max    int
		names       []string
	}{
		{"month", parts[3], 1, 12, monthNames}, {"day", parts[2], 1, 31, nil},
		{"day_of_week", parts[4], 0, 6, weekdayNames}, {"hour", parts[1], 0, 23, nil}, {"minute", parts[0], 0, 59, nil},
	} {
		// SEPARATOR is re.compile(" *, *"); only the entire field uses str.strip.
		terms := strings.Split(pyvalue.Strip(field.value), ",")
		for i, term := range terms {
			if i > 0 {
				term = strings.TrimLeft(term, " ")
			}
			if i < len(terms)-1 {
				term = strings.TrimRight(term, " ")
			}
			if err := sourceCronTermError(field.name, term, field.min, field.max, field.names); err != nil {
				return err
			}
		}
	}
	return nil
}
func sourceCronTermError(field, term string, min, max int, names []string) error {
	var parseErr error
	integer := func(text string) *big.Int {
		if text == "" {
			return nil
		}
		v, err := pyvalue.Integer(text)
		if err != nil {
			parseErr = err
		}
		return v
	}
	var first, last, step *big.Int
	all := false
	if m := cronAllDiagnostic.FindStringSubmatch(term); m != nil {
		all = true
		step = integer(m[1])
	} else if m := cronRangeDiagnostic.FindStringSubmatch(term); m != nil {
		first, last, step = integer(m[1]), integer(m[2]), integer(m[3])
		if parseErr != nil {
			return parseErr
		}
		if last == nil && step == nil {
			last = new(big.Int).Set(first)
		}
	} else if len(names) > 0 {
		if m := cronNameDiagnostic.FindStringSubmatch(term); m != nil {
			category := "weekday"
			if field == "month" {
				category = "month"
			}
			lookup := func(text string) (*big.Int, error) {
				name := pyvalue.LowerCase(text)
				for i, item := range names {
					if name == item {
						return big.NewInt(int64(min + i)), nil
					}
				}
				return nil, fmt.Errorf("Invalid %s name \"%s\"", category, text)
			}
			var err error
			first, err = lookup(m[1])
			if err != nil {
				return err
			}
			last = new(big.Int).Set(first)
			if m[2] != "" {
				last, err = lookup(m[2])
				if err != nil {
					return err
				}
			}
		} else {
			return fmt.Errorf("Unrecognized expression \"%s\" for field \"%s\"", term, field)
		}
	} else if field == "day" && cronLastDiagnostic.MatchString(term) {
		return nil
	} else {
		return fmt.Errorf("Unrecognized expression \"%s\" for field \"%s\"", term, field)
	}
	if parseErr != nil {
		return parseErr
	}
	if step != nil && step.Sign() == 0 {
		return fmt.Errorf("Increment must be higher than 0")
	}
	if first != nil && last != nil && first.Cmp(last) > 0 {
		return fmt.Errorf("The minimum value in a range must not be higher than the maximum")
	}
	wrap := func(reason string) error {
		return fmt.Errorf("Error validating expression %s: %s", sourceRepr(term), reason)
	}
	span := big.NewInt(int64(max - min))
	if step != nil && step.Cmp(span) > 0 {
		return wrap(fmt.Sprintf("the step value (%s) is higher than the total range of the expression (%s)", step, span))
	}
	if all {
		return nil
	}
	if first.Cmp(big.NewInt(int64(min))) < 0 {
		return wrap(fmt.Sprintf("the first value (%s) is lower than the minimum value (%d)", first, min))
	}
	if last != nil && last.Cmp(big.NewInt(int64(max))) > 0 {
		return wrap(fmt.Sprintf("the last value (%s) is higher than the maximum value (%d)", last, max))
	}
	end := big.NewInt(int64(max))
	if last != nil && last.Sign() != 0 {
		end.Set(last)
	}
	span.Sub(end, first)
	if step != nil && step.Cmp(span) > 0 {
		return wrap(fmt.Sprintf("the step value (%s) is higher than the total range of the expression (%s)", step, span))
	}
	return nil
}

// Only numeric compiler tokens are normalized; named prefixes retain the
// original APS matching rules and the diagnostic keeps the original spelling.
func normalizeCronDecimalField(value string) string {
	parts := strings.Split(value, ",")
	for i, term := range parts {
		if cronAllDiagnostic.MatchString(term) || cronRangeDiagnostic.MatchString(term) {
			var b strings.Builder
			for _, r := range term {
				if v, err := pyvalue.Integer(string(r)); err == nil {
					b.WriteString(v.String())
				} else {
					b.WriteRune(r)
				}
			}
			parts[i] = b.String()
		}
	}
	return strings.Join(parts, ",")
}
