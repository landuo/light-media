package zhconv

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
)

const sourceOracleSHA256 = "fb4e540a618e575f7114d3e2a0055050e3fcfbf25a139323385351e40dabe3bb"

type sourceCase struct {
	Input struct {
		ID    string `json:"id"`
		Group string `json:"group"`
		Text  string `json:"text"`
	} `json:"input"`
	InputSHA string `json:"input_utf8_sha256"`
	Outcome  struct {
		Kind    string `json:"kind"`
		Value   string `json:"value"`
		Bytes   int    `json:"utf8_bytes"`
		Base64  string `json:"utf8_base64"`
		SHA     string `json:"utf8_sha256"`
		Class   string `json:"class"`
		Message string `json:"message"`
	} `json:"outcome"`
}

func hashText(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func sourceCases(t *testing.T) []sourceCase {
	t.Helper()
	b, err := os.ReadFile("testdata/source4789.json")
	if err != nil {
		t.Fatal(err)
	}
	if hashText(string(b)) != sourceOracleSHA256 {
		t.Fatal("source oracle bytes changed")
	}
	var doc struct {
		Count int               `json:"case_count"`
		Cases []sourceCase      `json:"cases"`
		Drift []json.RawMessage `json:"input_drift"`
	}
	if err = json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Count != 4789 || len(doc.Cases) != 4789 || len(doc.Drift) != 0 {
		t.Fatal("source oracle counts/drift")
	}
	for _, c := range doc.Cases {
		if hashText(c.Input.Text) != c.InputSHA {
			t.Fatalf("source input hash %s", c.Input.ID)
		}
		if c.Outcome.Kind == "return" && (hashText(c.Outcome.Value) != c.Outcome.SHA || len(c.Outcome.Value) != c.Outcome.Bytes || base64.StdEncoding.EncodeToString([]byte(c.Outcome.Value)) != c.Outcome.Base64) {
			t.Fatalf("source output encoding %s", c.Input.ID)
		}
	}
	return doc.Cases
}

func unorderedDescriptionID(id string) bool {
	switch id {
	case "description-multi", "description-unid", "description-repeat-0", "description-repeat-1", "description-repeat-2":
		return true
	}
	return false
}

// This root checks every ordered return and the one observed source exception.
// The exact five unordered descriptions have a separate, explicit comparator;
// none is silently sorted, counted as byte-equal or marked t.Skip.
func TestHansOrderedSourceOracle(t *testing.T) {
	returns, exceptions, descriptions := 0, 0, 0
	for _, c := range sourceCases(t) {
		if unorderedDescriptionID(c.Input.ID) {
			descriptions++
			continue
		}
		got, err := Hans(c.Input.Text)
		switch c.Outcome.Kind {
		case "return":
			returns++
			if err != nil || got != c.Outcome.Value {
				t.Errorf("%s: got %q error=%v; want %q", c.Input.ID, got, err, c.Outcome.Value)
			}
		case "exception":
			exceptions++
			var sourceErr *SourcePanicError
			if got != "" || !errors.As(err, &sourceErr) || sourceErr.SourceClass() != c.Outcome.Class || sourceErr.Error() != c.Outcome.Message {
				t.Errorf("%s: got %q error=%v; want %s: %s", c.Input.ID, got, err, c.Outcome.Class, c.Outcome.Message)
			}
		default:
			t.Fatalf("unexpected source outcome %s", c.Input.ID)
		}
	}
	if returns != 4783 || exceptions != 1 || descriptions != 5 {
		t.Fatalf("coverage returns=%d exceptions=%d separateDescriptions=%d", returns, exceptions, descriptions)
	}
	t.Logf("exact source comparisons: %d returns + %d exception; %d D cases explicitly separate", returns, exceptions, descriptions)
}

// Bounded candidate comparison for ONLY five original D observations. This is
// not byte-order equality. It preserves every source clause byte, label,
// punctuation and multiplicity, with at most 3 clauses / 6 permutations.
// Its acceptance is independent-review gated; source raw is never normalized.
func TestHansBoundedDescriptionSourceComparison(t *testing.T) {
	count := 0
	for _, c := range sourceCases(t) {
		if !unorderedDescriptionID(c.Input.ID) {
			continue
		}
		count++
		want := c.Outcome.Value
		var clauses []string
		if c.Input.ID == "description-unid" {
			// This exact fixture contains two fixed unidirectional groups with
			// no delimiter between groups. Find their actual raw boundaries.
			a, b := "Owned⇒zh-hans: ", "Other⇒zh-cn: "
			if strings.Count(want, a) != 1 || strings.Count(want, b) != 1 {
				t.Fatal("unexpected original unid description shape")
			}
			i, j := strings.Index(want, a), strings.Index(want, b)
			if i == 0 && j > 0 {
				clauses = []string{want[:j], want[j:]}
			} else if j == 0 && i > 0 {
				clauses = []string{want[:i], want[i:]}
			} else {
				t.Fatal("unexpected original unid boundaries")
			}
		} else {
			if !strings.HasSuffix(want, "；") {
				t.Fatal("original D punctuation changed")
			}
			parts := strings.Split(want, "；")
			if len(parts) != 4 || parts[3] != "" {
				t.Fatal("original D must have exactly 3 clauses")
			}
			for _, p := range parts[:3] {
				clauses = append(clauses, p+"；")
			}
		}
		got, err := Hans(c.Input.Text)
		if err != nil || !exactClausePermutation(got, clauses) {
			t.Errorf("%s: source raw %q target raw %q err=%v", c.Input.ID, want, got, err)
		}
		t.Logf("%s: source_raw=%q target_raw=%q byte_equal=%t; comparison=bounded_clause_permutation_not_order", c.Input.ID, want, got, got == want)
	}
	if count != 5 {
		t.Fatalf("description count %d", count)
	}
}

func exactClausePermutation(got string, clauses []string) bool {
	if len(clauses) == 0 {
		return got == ""
	}
	for i, c := range clauses {
		if !strings.HasPrefix(got, c) {
			continue
		}
		rest := make([]string, 0, len(clauses)-1)
		rest = append(rest, clauses[:i]...)
		rest = append(rest, clauses[i+1:]...)
		if exactClausePermutation(got[len(c):], rest) {
			return true
		}
	}
	return false
}

func TestHansConcurrentIsolation(t *testing.T) {
	var selected []sourceCase
	for _, c := range sourceCases(t) {
		switch c.Input.ID {
		case "plain-mixed", "hidden-after-text", "remove-builtin-replacement", "replacement-not-reconverted", "nested", "html-global-rule-visible":
			selected = append(selected, c)
		}
	}
	if len(selected) != 6 {
		t.Fatal("concurrency source subset missing")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 8; j++ {
				for _, c := range selected {
					got, err := Hans(c.Input.Text)
					if err != nil || got != c.Outcome.Value {
						t.Errorf("%s: %q err=%v", c.Input.ID, got, err)
					}
				}
			}
		}()
	}
	wg.Wait()
	if _, err := Hans(string([]byte{0xff})); !errors.Is(err, ErrInvalidUTF8) {
		t.Fatal("Go invalid UTF-8 boundary not explicit")
	}
}
