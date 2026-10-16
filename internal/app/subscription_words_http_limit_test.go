package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"light-media/internal/media/words"
)

func TestSubscriptionWordsLargeHTTPFormPersistsEditsAndReopens(t *testing.T) {
	a, ctx := subscriptionTestApp(t), context.Background()
	// Persist the fixture's real connection/root choices as well, so editing
	// after New exercises the same valid form rather than a missing downloader.
	source, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	settings := a.Settings()
	settings.AllowedRoots = []string{source}
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	cookie := setupTestAdmin(t, a)
	text := strings.Repeat("#"+strings.Repeat("large-source-comment", 3000)+"\n", 42)
	request := subscriptionRequest(t)
	request.DownloadPath, request.RootID = source, ""
	request.CustomWords = subscriptionWordsRaw(t, text)
	body := subscriptionWordsRequestBody(t, request)
	if len(body) <= 2<<20 || len(body) >= subscriptionBodyLimit || len(text) > words.MaxBytes {
		t.Fatal("fixture does not exercise a valid form above the ordinary body limit")
	}
	sub := subscriptionWordsAPIWrite(t, a, cookie, "", request, text)
	subscriptionWordsAPIRead(t, a, cookie, sub.ID, text)
	sealed := subscriptionWordsCiphertext(t, a, sub.ID)
	if json.Valid(sealed) || bytes.Contains(sealed, []byte("large-source-comment")) {
		t.Fatal("large form was not stored privately")
	}
	edited := text + "\n# original spacing retained  \n"
	request.Revision, request.CustomWords = sub.Revision, subscriptionWordsRaw(t, edited)
	sub = subscriptionWordsAPIWrite(t, a, cookie, sub.ID, request, edited)
	subscriptionWordsAPIRead(t, a, cookie, sub.ID, edited)
	beforeRestart := subscriptionWordsCiphertext(t, a, sub.ID)
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	subscriptionWordsAPIRead(t, reopened, cookie, sub.ID, edited)
	if current, err := reopened.subscriptionWords(ctx, sub.ID); err != nil || current != edited || !bytes.Equal(subscriptionWordsCiphertext(t, reopened, sub.ID), beforeRestart) {
		t.Fatal("large encoded form did not survive an actual process reopen", err)
	}
	// A further large PUT must still work after loading the persisted settings
	// and encrypted word value, with ordinary optimistic revision protection.
	edited += "# edited after reopen\n"
	request.Revision, request.CustomWords = sub.Revision, subscriptionWordsRaw(t, edited)
	sub = subscriptionWordsAPIWrite(t, reopened, cookie, sub.ID, request, edited)
	subscriptionWordsAPIRead(t, reopened, cookie, sub.ID, edited)
}

func TestSubscriptionWordsHTTPBoundsRejectWithoutChangingCurrentValue(t *testing.T) {
	a, ctx := subscriptionTestApp(t), context.Background()
	cookie := setupTestAdmin(t, a)
	request := subscriptionRequest(t)
	text := "# retain the existing private value\n"
	request.CustomWords = subscriptionWordsRaw(t, text)
	sub := subscriptionWordsAPIWrite(t, a, cookie, "", request, text)
	sealed := subscriptionWordsCiphertext(t, a, sub.ID)
	request.Revision, request.CustomWords = sub.Revision, nil
	base := strings.TrimSuffix(subscriptionWordsRequestBody(t, request), "}")
	assertUnchanged := func(t *testing.T) {
		t.Helper()
		var current Subscription
		if err := a.Store.Get(ctx, "subscription", sub.ID, &current); err != nil || !reflect.DeepEqual(current, sub) || !bytes.Equal(subscriptionWordsCiphertext(t, a, sub.ID), sealed) {
			t.Fatal("rejected HTTP form changed the revision or encrypted words", err)
		}
	}
	for _, item := range []struct {
		name, text, code string
	}{
		{"raw_word_list", strings.Repeat("#"+strings.Repeat("x", 60<<10)+"\n", 137), "INVALID_SUBSCRIPTION_WORDS"},
		{"escaped_word_value", strings.Repeat("#"+strings.Repeat("\x01", 60<<10)+"\n", 46), "INVALID_SUBSCRIPTION_WORDS"},
	} {
		t.Run(item.name, func(t *testing.T) {
			raw := subscriptionWordsRaw(t, item.text)
			body := base + `,"custom_words":` + string(raw) + `}`
			if len(body) >= subscriptionBodyLimit {
				t.Fatal("field boundary fixture is being rejected at the body limit")
			}
			if item.name == "raw_word_list" && len(item.text) <= words.MaxBytes || item.name == "escaped_word_value" && (len(item.text) > words.MaxBytes || len(raw) <= pluginDataValueLimit) {
				t.Fatal("fixture does not isolate its declared field limit")
			}
			response := callTest(t, a.Handler(), "PUT", "/api/subscriptions/"+sub.ID, body, cookie, "")
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), item.code) {
				t.Fatal("oversized local word value was not rejected", response.Code)
			}
			assertUnchanged(t)
		})
	}
	for _, item := range []struct{ name, body string }{
		{"request_limit", base + `,"ignored_padding":"` + strings.Repeat("x", subscriptionBodyLimit) + `"}`},
		{"second_object", base + `} {}`},
	} {
		t.Run(item.name, func(t *testing.T) {
			response := callTest(t, a.Handler(), "PUT", "/api/subscriptions/"+sub.ID, item.body, cookie, "")
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "INVALID_JSON") {
				t.Fatal("subscription decoder lost its bounded single-object contract", response.Code)
			}
			assertUnchanged(t)
		})
	}
	// The specialized allowance must not expand small unrelated write routes.
	response := callTest(t, a.Handler(), "POST", "/api/control", `{"paused":false,"ignored_padding":"`+strings.Repeat("x", (2<<20)+1)+`"}`, cookie, "")
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "INVALID_JSON") {
		t.Fatal("subscription body allowance escaped to the ordinary decoder", response.Code)
	}
	assertUnchanged(t)
	subscriptionWordsAPIRead(t, a, cookie, sub.ID, text)
}
