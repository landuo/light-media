package autosignin

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"testing"
)

type mteamFixtureTransport struct {
	t     *testing.T
	input Object
	calls []any
}

func (f *mteamFixtureTransport) Post(_ context.Context, plan RequestPlan) (*AdapterResponse, error) {
	var proxy any
	if plan.UseProxy {
		proxy = Object{"https": "synthetic-proxy-reference"}
	}
	f.calls = append(f.calls, Object{"headers": plan.Headers, "timeout": plan.Timeout, "proxies": proxy, "url": plan.URL, "data": plan.Data})
	value, ok := f.input["response"].(Object)
	if !ok {
		return nil, nil
	}
	response := &AdapterResponse{Status: 200, Payload: value["payload"]}
	if status, ok := value["status"].(json.Number); ok {
		x, _ := status.Int64()
		response.Status = int(x)
	}
	if Truth(value["json_error"]) {
		response.JSONError = errors.New("synthetic invalid JSON")
	}
	return response, nil
}
func TestMTeamActualSourceCryptoPlanPayloadAndVisit(t *testing.T) {
	raw, err := readAutoSignInFixture("mteam-reference.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Cases []struct {
			Name, Kind, Error string
			Input, Expected   any
		}
	}
	if Decode(raw, &reference) != nil || len(reference.Cases) != 37 {
		t.Fatal("reference inventory")
	}
	for _, c := range reference.Cases {
		t.Run(c.Name, func(t *testing.T) {
			var got any
			switch c.Kind {
			case "headers":
				d := c.Input.(Object)
				stamp, err := d["timestamp_ms"].(json.Number).Int64()
				if err != nil {
					t.Fatal(err)
				}
				got = Object{"headers": MTeamHeaders(d["url"].(string), d["token"].(string), d["ua"].(string), stamp), "sign": MTeamSign(stamp)}
			case "payload":
				state, message := MTeamPayload(c.Input)
				got = []any{state, message}
			case "match":
				got = MTeamMatch(c.Input.(Object)["url"].(string))
			case "signin":
				d := c.Input.(Object)
				site := d["site"].(Object)
				get := func(key string) string { v, _ := site[key].(string); return v }
				transport := &mteamFixtureTransport{t: t, input: d, calls: []any{}}
				state, message, err := MTeamVisit(context.Background(), Credentials{URL: get("url"), Token: get("token"), UserAgent: get("ua"), Proxy: site["proxy"], Timeout: site["timeout"]}, 1792152000125, transport)
				if err != nil {
					t.Fatal(err)
				}
				got = Object{"result": []any{state, message}, "calls": transport.calls}
			default:
				t.Fatal("unhandled source case")
			}
			if !reflect.DeepEqual(canonical(got), c.Expected) {
				t.Fatalf("source adapter differs\ngot=%s\nwant=%s", mustJSON(got), mustJSON(c.Expected))
			}
		})
	}
}
func mustJSON(v any) []byte { raw, _ := json.Marshal(v); return raw }
func TestMTeamConfirmedLoginDoesNotClaimSignInAndNoUnboundSuccess(t *testing.T) {
	state, message := MTeamPayload(Object{"code": "1", "message": "FAIL", "data": nil})
	result := SiteResult("Synthetic", state, message, "签到")
	if result.Success || !result.LoggedIn || !result.Completed() {
		t.Fatal("login-only adapter claimed confirmed attendance")
	}
	if _, _, err := MTeamVisit(context.Background(), Credentials{URL: "https://kp.m-team.cc", Token: "synthetic-only"}, 1, nil); err == nil {
		t.Fatal("unbound transport returned success")
	}
	if !MTeamMatch("https://foo.m-team.cc.evil.invalid/") {
		t.Fatal("source raw matcher changed")
	}
	// The source match is deliberately not a credential authorization. There
	// is no HTTP/RPC implementation in this package to grant that host access.
}

func TestMTeamASCIITargetUpperMappingsFromActualPythonScan(t *testing.T) {
	raw, err := readAutoSignInFixture("mteam-reference.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Provenance struct {
			Upper map[string]string `json:"ascii_upper"`
		}
	}
	if Decode(raw, &reference) != nil || len(reference.Provenance.Upper) != 10 {
		t.Fatal("incomplete actual Python15 scan")
	}
	for value, want := range reference.Provenance.Upper {
		r, err := strconv.ParseInt(value, 10, 32)
		if err != nil {
			t.Fatal(err)
		}
		if got := sourceASCIIUpper(string(rune(r))); got != want {
			t.Fatal("ASCII-target uppercase differs", value, got, want)
		}
	}
}
