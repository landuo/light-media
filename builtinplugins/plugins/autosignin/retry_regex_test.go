package autosignin

import (
	"fmt"
	"os"
	"testing"
)

func TestActualPythonRetryRegex35(t *testing.T) {
	raw, err := os.ReadFile("reference/retry-regex35.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Cases []struct {
			Pattern, Text, Exception, Message string
			Matched                           bool
		}
	}
	if err = Decode(raw, &source); err != nil {
		t.Fatal(err)
	}
	if len(source.Cases) != 35 {
		t.Fatal(len(source.Cases))
	}
	for i, c := range source.Cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			got, err := VerifiedRetryMatch(c.Pattern, c.Text)
			if c.Exception != "" {
				if err == nil {
					t.Fatalf("source compilation fails, target returned %v for %q", got, c.Pattern)
				}
				if err.Error() != c.Message {
					t.Fatalf("error got %q want original %q", err.Error(), c.Message)
				}
				return
			}
			if err != nil || got != c.Matched {
				t.Fatalf("%q/%q: got %v,%v source %v", c.Pattern, c.Text, got, err, c.Matched)
			}
		})
	}
}

func TestActualPythonRetryLiterals20(t *testing.T) {
	raw, err := os.ReadFile("reference/retry-literals20.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Cases []struct {
			Pattern, Text string
			Matched       bool
			Error         Object
		}
	}
	if err = Decode(raw, &source); err != nil {
		t.Fatal(err)
	}
	if len(source.Cases) != 20 {
		t.Fatal(len(source.Cases))
	}
	for i, c := range source.Cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			got, err := VerifiedRetryMatch(c.Pattern, c.Text)
			if c.Error != nil {
				if err == nil {
					t.Fatal("original rejects pattern")
				}
				return
			}
			if err != nil || got != c.Matched {
				t.Fatalf("%q/%q: got %v,%v want original %v", c.Pattern, c.Text, got, err, c.Matched)
			}
		})
	}
}

func TestActualPythonRetryModes20(t *testing.T) {
	raw, err := os.ReadFile("reference/retry-modes20.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Cases []struct {
			Pattern, Text string
			Matched       bool
			Error         Object
		}
	}
	if err = Decode(raw, &source); err != nil {
		t.Fatal(err)
	}
	if len(source.Cases) != 20 {
		t.Fatal(len(source.Cases))
	}
	for i, c := range source.Cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			got, err := VerifiedRetryMatch(c.Pattern, c.Text)
			if c.Error != nil {
				if err == nil {
					t.Fatal("original rejects pattern")
				}
				return
			}
			if err != nil || got != c.Matched {
				t.Fatalf("%q/%q: got %v,%v want original %v", c.Pattern, c.Text, got, err, c.Matched)
			}
		})
	}
}

func TestActualPythonRetryBackref12(t *testing.T) {
	raw, err := os.ReadFile("reference/retry-backref12.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Cases []struct {
			Pattern, Text string
			Matched       bool
			Error         Object
		}
	}
	if err = Decode(raw, &source); err != nil {
		t.Fatal(err)
	}
	if len(source.Cases) != 12 {
		t.Fatal(len(source.Cases))
	}
	for i, c := range source.Cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			got, err := VerifiedRetryMatch(c.Pattern, c.Text)
			if c.Error != nil {
				if err == nil {
					t.Fatal("original rejects pattern")
				}
				if err.Error() != c.Error["message"] {
					t.Fatalf("error got %q want actual %v", err.Error(), c.Error["message"])
				}
				return
			}
			if err != nil || got != c.Matched {
				t.Fatalf("%q/%q: got %v,%v want original %v", c.Pattern, c.Text, got, err, c.Matched)
			}
		})
	}
}

func TestActualPythonRetryLexer114(t *testing.T) {
	raw, err := os.ReadFile("reference/retry-lexer114.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Cases []struct {
			Pattern string
			Error   Object
		}
	}
	if err = Decode(raw, &source); err != nil {
		t.Fatal(err)
	}
	if len(source.Cases) != 114 {
		t.Fatal(len(source.Cases))
	}
	for i, c := range source.Cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			_, err := VerifiedRetryMatch(c.Pattern, "a")
			if (err != nil) != (c.Error != nil) {
				t.Fatalf("%q: got %v want original %v", c.Pattern, err, c.Error)
			}
		})
	}
}

func TestActualPythonRetryScope8(t *testing.T) {
	raw, err := os.ReadFile("reference/retry-scope8.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Cases []struct {
			Pattern, Text string
			Matched       bool
			Error         Object
		}
	}
	if err = Decode(raw, &source); err != nil {
		t.Fatal(err)
	}
	if len(source.Cases) != 8 {
		t.Fatal(len(source.Cases))
	}
	for i, c := range source.Cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			got, err := VerifiedRetryMatch(c.Pattern, c.Text)
			if c.Error != nil {
				if err == nil {
					t.Fatal("original rejects pattern")
				}
				return
			}
			if err != nil || got != c.Matched {
				t.Fatalf("%q/%q: got %v,%v want original %v", c.Pattern, c.Text, got, err, c.Matched)
			}
		})
	}
}
