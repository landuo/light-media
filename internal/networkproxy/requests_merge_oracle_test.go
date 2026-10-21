package networkproxy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"sort"
	"testing"
)

// This compares only the explicitly bounded transport scope. Unsupported source
// cases are evidence, not claimed Go parity; no Python executes in these tests.
func TestRequestsMergeFrozenSourceOracle(t *testing.T) {
	const dir = "../../testdata/plugins/auto-signin-proxy-routing/"
	var fixture struct {
		SourceSHA256 string `json:"source_sha256"`
		Cases        []struct {
			Name        string             `json:"name"`
			Environment map[string]string  `json:"environment"`
			Explicit    map[string]*string `json:"explicit"`
			Supported   bool               `json:"go_supported"`
			Expected    []struct {
				URL      string            `json:"url"`
				Proxies  map[string]string `json:"proxies"`
				Selected *string           `json:"selected"`
			} `json:"expected"`
		} `json:"cases"`
	}
	raw, err := os.ReadFile(dir + "oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(dir + "frozen-source.json")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(source)
	if hex.EncodeToString(sum[:]) != fixture.SourceSHA256 {
		t.Fatal("oracle source digest mismatch")
	}
	compared, excluded, hops := 0, 0, 0
	for _, c := range fixture.Cases {
		if !c.Supported {
			excluded++
			continue
		}
		compared++
		t.Run(c.Name, func(t *testing.T) {
			env := make([]string, 0, len(c.Environment))
			for key, value := range c.Environment {
				env = append(env, key+"="+value)
			}
			sort.Strings(env)
			explicit := map[string]string{}
			for key, value := range c.Explicit {
				if value == nil {
					t.Fatal("supported fixture has null-valued entry")
				}
				explicit[key] = *value
			}
			if len(c.Expected) == 0 {
				t.Fatal("missing source result")
			}
			initial, err := url.Parse(c.Expected[0].URL)
			if err != nil {
				t.Fatal(err)
			}
			transport, err := NewRequestsProxyTransport(&http.Transport{}, env, explicit, initial)
			if err != nil {
				t.Fatal(err)
			}
			defer transport.CloseIdleConnections()
			for index, expected := range c.Expected {
				hops++
				target, err := url.Parse(expected.URL)
				if err != nil {
					t.Fatal(err)
				}
				selected, err := transport.requestProxy(target)
				if err != nil {
					t.Fatalf("hop %d: %v", index, err)
				}
				want := ""
				if expected.Selected != nil {
					want = *expected.Selected
				}
				got := ""
				if selected != nil {
					got = selected.String()
				}
				if got != want {
					t.Errorf("hop %d selected = %q; source = %q", index, got, want)
				}
				if !reflect.DeepEqual(transport.merged.proxies, expected.Proxies) {
					t.Errorf("hop %d mapping = %#v; source = %#v", index, transport.merged.proxies, expected.Proxies)
				}
			}
		})
	}
	if compared != 85 || excluded != 15 {
		t.Fatalf("unexpected bounded coverage: compared=%d excluded=%d", compared, excluded)
	}
	t.Logf("frozen-source synthetic coverage: %d cases / %d hops compared, %d source-only cases excluded", compared, hops, excluded)
}
