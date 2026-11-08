package schedule

import (
	"encoding/json"
	"os"
	"testing"
)

func TestCrontabLexerMatchesCapturedPythonSplit(t *testing.T) {
	var oracle struct {
		Cases []struct {
			Case, Expression, Error string
			Fields                  struct {
				Minute, Hour, Day, Month string
				DayOfWeek                string `json:"day_of_week"`
			}
		}
	}
	raw, err := os.ReadFile("../../../testdata/plugin-cron-whitespace-20261105/source-oracle.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 7 {
		t.Fatal("captured source lexer oracle", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Case, func(t *testing.T) {
			fields, err := crontabFields(sample.Expression)
			if sample.Error != "" {
				if err == nil {
					t.Fatal("source rejected separator accepted")
				}
				return
			}
			if err != nil || fields.Minute != sample.Fields.Minute || fields.Hour != sample.Fields.Hour || fields.Day != sample.Fields.Day || fields.Month != sample.Fields.Month || fields.DayOfWeek != sample.Fields.DayOfWeek {
				t.Fatal("Python split/mapping differs", fields, err)
			}
			if _, err := ParseCrontab(sample.Expression, "UTC"); err != nil {
				t.Fatal("source expression did not reach calendar", err)
			}
		})
	}
}
