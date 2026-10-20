package notice

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Reviewed notification-template extension. The pinned original manifest and
// all frozen original inputs remain intact; these three source paths extend
// ordered assignment and enum provenance. See noticetemplate source oracles.
var reviewedTemplateSources = map[string]string{
	"internal/notice/envelope.go":            "7ef13adc6693df053ba2454b32e42dc5e8f10fd001fe0f1bb972a3b7f93cc22d",
	"internal/notice/router.go":              "bf5016570dd0bff7a987d53742edbe74ef61016fc8ee4a1992556df3bf651d81",
	"internal/notice/template_assignment.go": "87212b6267257f398163d86ab12cb5a793567574800d385fd8e50f7b4191dbe8",
}

// These receipts pin a pure behavior core. They do not assert App registration,
// persistent routing/queue recovery, recipient grants or an actual delivery.
func TestFormalNoticeSourceInventory(t *testing.T) {
	root := filepath.Clean("../..")
	raw, err := os.ReadFile(filepath.Join(root, "testdata/notice-message-core/formal-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	if hex.EncodeToString(hash[:]) != "82a017e210606cb5a860783b0f213109a2757a8e3d026b44e010963cb27d2f6b" {
		t.Fatal("formal notice manifest changed without review")
	}
	var m struct {
		Version       int
		Files, Inputs map[string]string
	}
	if json.Unmarshal(raw, &m) != nil || m.Version != 1 || len(m.Files) != 11 || len(m.Inputs) != 45 {
		t.Fatal("formal notice inventory incomplete", len(m.Files), len(m.Inputs))
	}
	for _, items := range []map[string]string{m.Files, m.Inputs} {
		for name, want := range items {
			if reviewed, ok := reviewedTemplateSources[name]; ok {
				want = reviewed
			}
			if filepath.IsAbs(name) || filepath.ToSlash(filepath.Clean(name)) != name || strings.HasPrefix(name, "../") {
				t.Fatal("unsafe manifest path", name)
			}
			b, err := os.ReadFile(filepath.Join(root, name))
			if err != nil {
				t.Fatal(name, err)
			}
			hash := sha256.Sum256(b)
			if hex.EncodeToString(hash[:]) != want {
				t.Fatal("source/reference changed", name)
			}
		}
	}
	for name, want := range reviewedTemplateSources {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != want {
			t.Fatal("reviewed template source changed", name)
		}
	}
	actual := 0
	err = filepath.WalkDir(".", func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || name == "formal_provenance_test.go" {
			return nil
		}
		if name == "template_assignment.go" {
			return nil
		}
		actual++
		if _, ok := m.Files["internal/notice/"+filepath.ToSlash(name)]; !ok {
			t.Fatal("unlisted formal file", name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if actual != len(m.Files) {
		t.Fatal("formal file inventory differs", actual)
	}
}

func TestFormalNoticeMatchesFrozenCandidate(t *testing.T) {
	raw, err := os.ReadFile(originalFixture("source-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != "5a5f2e2851acc6dc14e93feafa99e7ea00ce508f79bbdba2b1a526391ff7b9f0" {
		t.Fatal("candidate manifest changed")
	}
	var m struct {
		SHA map[string]string `json:"sha256"`
	}
	if json.Unmarshal(raw, &m) != nil || len(m.SHA) != 40 {
		t.Fatal("candidate manifest incomplete")
	}
	for name, want := range m.SHA {
		b, err := os.ReadFile(originalFixture(name))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != want {
			t.Fatal("frozen candidate changed", name)
		}
	}
	for _, name := range []string{"envelope.go", "router.go", "schedule.go", "queue.go", "pydantic_int.go", "original_test.go", "contract_test.go"} {
		if _, reviewed := reviewedTemplateSources["internal/notice/"+name]; reviewed {
			continue
		}
		b, err := os.ReadFile(originalFixture(name))
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		if strings.HasSuffix(name, "_test.go") {
			s = strings.ReplaceAll(s, `os.Open("reference/installed-oracle.json")`, `os.Open(originalFixture("reference/installed-oracle.json"))`)
			s = strings.ReplaceAll(s, `os.ReadFile("reference/source/" + local)`, `os.ReadFile(originalFixture("reference/source/" + local))`)
		}
		adapted, err := format.Source([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		formal, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(adapted, formal) {
			t.Fatal("unlisted path or behavior change", name)
		}
	}
	for input, target := range map[string]string{"reference/source/pydantic_LICENSE": "LICENSE-PYDANTIC.txt", "reference/source/jiter-0.14.0/LICENSE": "LICENSE-JITER.txt"} {
		a, err := os.ReadFile(originalFixture(input))
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(target)
		if err != nil || !bytes.Equal(a, b) {
			t.Fatal("license differs", target, err)
		}
	}
}

func TestFormalNoticeOriginalCorpusInventory(t *testing.T) {
	want := map[string]int{"envelopes": 353, "periods": 19, "windows": 84, "queue": 6, "monitor": 6, "routes": 18, "route_failures": 16, "async_queue": 6, "time_minutes": 11, "coercions": 794, "integrated": 3}
	o := oracle(t)
	total := 0
	for name, n := range want {
		values, ok := o[name].([]any)
		if !ok || len(values) != n {
			t.Fatal("original corpus changed", name, len(values), n)
		}
		total += len(values)
	}
	if total != 1316 {
		t.Fatal("source case count", total)
	}
}
