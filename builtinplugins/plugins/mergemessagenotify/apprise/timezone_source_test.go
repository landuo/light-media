package apprise

import (
	"encoding/json"
	"os"
	"testing"
)

func TestSourceTimezones199(t *testing.T) {
	data, err := os.ReadFile("testdata/timezone-1.9.9.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name, URL string
			Added     bool
		}
	}
	if err := json.Unmarshal(data, &fixture); err != nil || len(fixture.Cases) == 0 {
		t.Fatalf("invalid timezone source fixture: %v", err)
	}
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			added, err := New(nil).Add(row.URL)
			if err != nil || added != row.Added {
				t.Fatalf("Add = %v, %v, source %v", added, err, row.Added)
			}
		})
	}
}
