package app

import (
	"encoding/json"
	"os"
	"testing"
)

func TestStandardUserMessageActualSDKSourceCloneFailure(t *testing.T) {
	path := os.Getenv("OWNED_USER_MESSAGE_FIXTURE_BINARY")
	if path == "" {
		t.Fatal("actual ordinary user-message SDK fixture required")
	}
	t.Setenv("OWNED_EVENT_FIXTURE_BINARY", path)
	raw, err := os.ReadFile("../../testdata/standard-subscription-events/user-message-oracle.json")
	var source struct {
		Cases []struct {
			Text   string
			Images json.RawMessage
			Events []ownedBusinessEvent
		}
	}
	if err != nil || json.Unmarshal(raw, &source) != nil || len(source.Cases) != 4 {
		t.Fatal("fixed ordinary UserMessage source", err)
	}
	a := newTestApp(t)
	cookie := installOwnedEventFixture(t, a)
	r := callTest(t, a.Handler(), "POST", "/api/plugins/"+ownedEventFixtureID+"/clone", `{"suffix":"User"}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	clone := ownedEventFixtureID + "user"
	setOwnedEventFixtureConfig(t, a, cookie, clone, true, false)
	setOwnedEventFixtureConfig(t, a, cookie, ownedEventFixtureID, true, true)
	for i, sample := range source.Cases {
		body, _ := json.Marshal(map[string]any{"text": sample.Text, "images": sample.Images, "userid": "forged", "source": "forged", "channel": "Telegram", "chat_id": "forged"})
		r = callTest(t, a.Handler(), "POST", "/api/v1/message/web", string(body), cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
		for _, id := range []string{ownedEventFixtureID, clone} {
			events := requireBusinessEventCount(t, a, id, "user.message", i+1)
			if !originalBaseJSONEqual(events[i].Data, sample.Events[0].Data) {
				t.Fatal("actual source/clone ordinary UserMessage fields", id, string(events[i].Data), string(sample.Events[0].Data))
			}
		}
	}
}
