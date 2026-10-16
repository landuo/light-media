package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func subscriptionWordsRaw(t *testing.T, text string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(text)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func subscriptionWordsRequestBody(t *testing.T, request SubscriptionRequest) string {
	t.Helper()
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func subscriptionWordsAPIWrite(t *testing.T, a *App, cookie *http.Cookie, key string, request SubscriptionRequest, wantWords string) Subscription {
	t.Helper()
	method, path := "POST", "/api/subscriptions"
	if key != "" {
		method, path = "PUT", path+"/"+key
	}
	response := callTest(t, a.Handler(), method, path, subscriptionWordsRequestBody(t, request), cookie, "")
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	var value map[string]json.RawMessage
	var sub Subscription
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &envelope) != nil || json.Unmarshal(envelope.Data, &value) != nil || json.Unmarshal(envelope.Data, &sub) != nil || sub.ID == "" {
		t.Fatal("subscription editor write failed", method, response.Code, response.Body.String())
	}
	var text string
	if raw, exists := value["custom_words"]; !exists || len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &text) != nil || text != wantWords {
		t.Fatal("subscription editor did not return the exact raw string", method)
	}
	return sub
}

func subscriptionWordsAPIRead(t *testing.T, a *App, cookie *http.Cookie, key, wantWords string) {
	t.Helper()
	response := callTest(t, a.Handler(), "GET", "/api/subscriptions", "", cookie, "")
	var envelope struct {
		Data struct {
			Items []map[string]json.RawMessage `json:"items"`
		} `json:"data"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &envelope) != nil {
		t.Fatal("subscription editor list failed", response.Code, response.Body.String())
	}
	for _, item := range envelope.Data.Items {
		var id, text string
		if json.Unmarshal(item["id"], &id) == nil && id == key {
			raw, exists := item["custom_words"]
			if !exists || len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &text) != nil || text != wantWords {
				t.Fatal("subscription list changed raw text, line endings or blank lines")
			}
			return
		}
	}
	t.Fatal("subscription editor list omitted the saved subscription")
}

func subscriptionWordsCiphertext(t *testing.T, a *App, key string) []byte {
	t.Helper()
	var sealed []byte
	if err := a.Store.DB.QueryRow(`SELECT sealed FROM subscription_words WHERE subscription_id=?`, key).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	return sealed
}

func TestSubscriptionWordsEditorRawRoundTripOmissionAndExplicitClear(t *testing.T) {
	a := subscriptionTestApp(t)
	ctx := context.Background()
	cookie := setupTestAdmin(t, a)
	text := "  # source whitespace\r\nsynthetic-private-subscription-marker => 原样替换\n\n  trailing space \t\n"
	request := subscriptionRequest(t)
	request.CustomWords = subscriptionWordsRaw(t, text)
	sub := subscriptionWordsAPIWrite(t, a, cookie, "", request, text)
	subscriptionWordsAPIRead(t, a, cookie, sub.ID, text)
	sealed := subscriptionWordsCiphertext(t, a, sub.ID)
	if json.Valid(sealed) || bytes.Contains(sealed, []byte("synthetic-private-subscription-marker")) {
		t.Fatal("subscription words were stored as plaintext")
	}
	// The private editor value must never become part of the ordinary core
	// record, public outbox fact or even the decrypted added-event identity.
	var core []byte
	if err := a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='subscription' AND id=?`, sub.ID).Scan(&core); err != nil {
		t.Fatal(err)
	}
	ordinary, _ := json.Marshal(sub)
	page, err := a.domainEvents(ctx, 0, 100)
	if err != nil || len(page.Events) != 1 || page.Events[0].Type != "subscription.added" {
		t.Fatal("real creation fact missing", err)
	}
	snapshot, err := a.subscriptionAddedSnapshot(ctx, page.Events[0])
	if err != nil {
		t.Fatal(err)
	}
	identity, _ := json.Marshal(snapshot)
	outbox, _ := json.Marshal(page)
	var sealedIdentity []byte
	if err := a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='subscription_added' AND id=?`, page.Events[0].EventID).Scan(&sealedIdentity); err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string][]byte{"core row": core, "ordinary subscription JSON": ordinary, "outbox": outbox, "decrypted event identity": identity, "sealed event identity": sealedIdentity} {
		if bytes.Contains(raw, []byte("custom_words")) || bytes.Contains(raw, []byte("synthetic-private-subscription-marker")) {
			t.Fatal("private editor words leaked outside their storage", name)
		}
	}
	// Old clients editing unrelated fields omit the key; that must retain both
	// the original text and the exact encrypted row rather than rewriting it.
	request.CustomWords, request.Revision, request.Keyword = nil, sub.Revision, "edited without words"
	sub = subscriptionWordsAPIWrite(t, a, cookie, sub.ID, request, text)
	if !bytes.Equal(subscriptionWordsCiphertext(t, a, sub.ID), sealed) {
		t.Fatal("omitted editor field rewrote its private value")
	}
	subscriptionWordsAPIRead(t, a, cookie, sub.ID, text)
	for _, clear := range []json.RawMessage{json.RawMessage(`null`), subscriptionWordsRaw(t, "")} {
		request.Revision, request.CustomWords = sub.Revision, clear
		sub = subscriptionWordsAPIWrite(t, a, cookie, sub.ID, request, "")
		subscriptionWordsAPIRead(t, a, cookie, sub.ID, "")
		var count int
		if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM subscription_words WHERE subscription_id=?`, sub.ID).Scan(&count); err != nil || count != 0 {
			t.Fatal("explicit clear retained a local override", count, err)
		}
		// Reseed between clear forms, proving that both null and an empty string
		// remove a present value rather than merely reading an already empty one.
		request.Revision, request.CustomWords = sub.Revision, subscriptionWordsRaw(t, text)
		sub = subscriptionWordsAPIWrite(t, a, cookie, sub.ID, request, text)
	}
	if current, err := a.subscriptionWords(ctx, sub.ID); err != nil || current != text {
		t.Fatal("private store disagreed with authenticated editor", err)
	}
}

func TestSubscriptionWordsAuthenticationAndInvalidValuesPreserveCurrentRevision(t *testing.T) {
	a := subscriptionTestApp(t)
	ctx := context.Background()
	cookie := setupTestAdmin(t, a)
	text := "synthetic-authenticated-words => kept\n\n"
	request := subscriptionRequest(t)
	request.CustomWords = subscriptionWordsRaw(t, text)
	sub := subscriptionWordsAPIWrite(t, a, cookie, "", request, text)
	sealed := subscriptionWordsCiphertext(t, a, sub.ID)
	request.Revision, request.CustomWords = sub.Revision, subscriptionWordsRaw(t, "unauthorized replacement")
	for _, operation := range []struct{ method, path, body string }{
		{"GET", "/api/subscriptions", ""},
		{"POST", "/api/subscriptions", subscriptionWordsRequestBody(t, request)},
		{"PUT", "/api/subscriptions/" + sub.ID, subscriptionWordsRequestBody(t, request)},
	} {
		response := callTest(t, a.Handler(), operation.method, operation.path, operation.body, nil, "")
		if response.Code != http.StatusUnauthorized || strings.Contains(response.Body.String(), "synthetic-authenticated-words") {
			t.Fatal("private editor was readable or writable without authentication", operation.method, response.Code)
		}
	}
	for name, raw := range map[string]json.RawMessage{
		"bool": json.RawMessage(`true`), "number": json.RawMessage(`17`), "object": json.RawMessage(`{}`), "array": json.RawMessage(`[]`),
		"oversize line":  subscriptionWordsRaw(t, strings.Repeat("x", (64<<10)+1)),
		"too many lines": subscriptionWordsRaw(t, strings.Repeat("\n", 100001)),
	} {
		t.Run(name, func(t *testing.T) {
			edit := request
			edit.CustomWords, edit.Keyword = raw, "must not commit"
			response := callTest(t, a.Handler(), "PUT", "/api/subscriptions/"+sub.ID, subscriptionWordsRequestBody(t, edit), cookie, "")
			if response.Code == http.StatusOK || !strings.Contains(response.Body.String(), "INVALID_SUBSCRIPTION_WORDS") {
				t.Fatal("invalid local words accepted or not classified", response.Code)
			}
			var current Subscription
			if err := a.Store.Get(ctx, "subscription", sub.ID, &current); err != nil || !reflect.DeepEqual(current, sub) || !bytes.Equal(subscriptionWordsCiphertext(t, a, sub.ID), sealed) {
				t.Fatal("rejected words changed the core revision or private value", err)
			}
		})
	}
	subscriptionWordsAPIRead(t, a, cookie, sub.ID, text)
}

func TestSubscriptionWordsInvalidUTF8RejectedBeforeJSONNormalization(t *testing.T) {
	a, ctx := subscriptionTestApp(t), context.Background()
	cookie := setupTestAdmin(t, a)
	text := "synthetic-valid-unicode => 保留\n"
	request := subscriptionRequest(t)
	request.CustomWords = subscriptionWordsRaw(t, text)
	sub := subscriptionWordsAPIWrite(t, a, cookie, "", request, text)
	sealed := subscriptionWordsCiphertext(t, a, sub.ID)
	request.Revision, request.CustomWords = sub.Revision, nil
	base := strings.TrimSuffix(subscriptionWordsRequestBody(t, request), "}")
	for _, invalid := range []string{"\xff", "\xc3("} {
		// Send original HTTP bytes, without a JSON encoder normalizing them first.
		body := base + `,"custom_words":"` + invalid + `"}`
		response := callTest(t, a.Handler(), "PUT", "/api/subscriptions/"+sub.ID, body, cookie, "")
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "INVALID_SUBSCRIPTION_WORDS") {
			t.Fatal("invalid original UTF-8 was normalized and accepted", response.Code)
		}
		var current Subscription
		if err := a.Store.Get(ctx, "subscription", sub.ID, &current); err != nil || !reflect.DeepEqual(current, sub) || !bytes.Equal(subscriptionWordsCiphertext(t, a, sub.ID), sealed) {
			t.Fatal("invalid UTF-8 consumed the core revision or private value", err)
		}
	}
	subscriptionWordsAPIRead(t, a, cookie, sub.ID, text)
}

func TestSubscriptionWordsCiphertextCannotMoveBetweenRealSubscriptionIDs(t *testing.T) {
	a := subscriptionTestApp(t)
	ctx := context.Background()
	request := subscriptionRequest(t)
	text := "first-private-identity => original"
	request.CustomWords = subscriptionWordsRaw(t, text)
	first, err := a.saveSubscription(ctx, "", request)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.cancelSubscription(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	request.CustomWords = subscriptionWordsRaw(t, "second-private-identity => independent")
	second, err := a.saveSubscription(ctx, "", request)
	if err != nil || second.ID == first.ID {
		t.Fatal("second real subscription was not independently created", err)
	}
	firstCiphertext := subscriptionWordsCiphertext(t, a, first.ID)
	if _, err = a.Store.DB.Exec(`UPDATE subscription_words SET sealed=? WHERE subscription_id=?`, firstCiphertext, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = a.subscriptionWords(ctx, second.ID); err == nil {
		t.Fatal("ciphertext from another subscription acquired the recipient identity")
	}
	if _, err = a.subscriptionEditorValue(ctx, second); err == nil {
		t.Fatal("editor hid a failed identity binding as empty or trusted text")
	}
	if own, err := a.subscriptionWords(ctx, first.ID); err != nil || own != text || !bytes.Equal(subscriptionWordsCiphertext(t, a, first.ID), firstCiphertext) {
		t.Fatal("cross-ID read changed the original private value", err)
	}
}

func TestSubscriptionWordsLateSQLFailureRollsBackCorePrivateValueAndOutbox(t *testing.T) {
	a := subscriptionTestApp(t)
	ctx := context.Background()
	request := subscriptionRequest(t)
	request.CustomWords = subscriptionWordsRaw(t, "synthetic-create-private => first")
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER deny_words_creation BEFORE INSERT ON domain_events BEGIN SELECT RAISE(ABORT,'synthetic late creation failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.saveSubscription(ctx, "", request); err == nil {
		t.Fatal("creation committed without its outbox fact")
	}
	for name, query := range map[string]string{
		"private words":     `SELECT COUNT(*) FROM subscription_words`,
		"core and identity": `SELECT COUNT(*) FROM records WHERE kind IN ('subscription','subscription_added')`,
		"outbox":            `SELECT COUNT(*) FROM domain_events`,
	} {
		var count int
		if err := a.Store.DB.QueryRow(query).Scan(&count); err != nil || count != 0 {
			t.Fatal("failed creation left a partial commit", name, count, err)
		}
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER deny_words_creation`); err != nil {
		t.Fatal(err)
	}
	sub, err := a.saveSubscription(ctx, "", request)
	if err != nil {
		t.Fatal("rolled-back creation did not permit exact retry", err)
	}
	sealed := subscriptionWordsCiphertext(t, a, sub.ID)
	beforeEvents, err := a.domainEvents(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	job, err := a.enqueue(ctx, "subscription", map[string]any{"id": sub.ID, "revision": sub.Revision}, "wf_words_rollback")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`CREATE TRIGGER deny_words_edit BEFORE UPDATE ON jobs WHEN OLD.kind='subscription' BEGIN SELECT RAISE(ABORT,'synthetic late edit failure'); END`); err != nil {
		t.Fatal(err)
	}
	for _, input := range []json.RawMessage{subscriptionWordsRaw(t, "must roll back replacement"), json.RawMessage(`null`), subscriptionWordsRaw(t, "")} {
		edit := request
		edit.Revision, edit.Keyword, edit.CustomWords = sub.Revision, "must roll back core revision", input
		if _, err = a.saveSubscription(ctx, sub.ID, edit); err == nil {
			t.Fatal("edit committed after its late SQL failure")
		}
		var current Subscription
		if err = a.Store.Get(ctx, "subscription", sub.ID, &current); err != nil || !reflect.DeepEqual(current, sub) || !bytes.Equal(subscriptionWordsCiphertext(t, a, sub.ID), sealed) {
			t.Fatal("edit replacement or clear escaped rollback", err)
		}
		var state string
		var generation int
		if err = a.Store.DB.QueryRow(`SELECT state,generation FROM jobs WHERE id=?`, job.ID).Scan(&state, &generation); err != nil || state != "queued" || generation != 0 {
			t.Fatal("failed edit invalidated the original check", state, generation, err)
		}
		if events, err := a.domainEvents(ctx, 0, 100); err != nil || !reflect.DeepEqual(events, beforeEvents) {
			t.Fatal("failed edit changed the creation outbox", err)
		}
	}
	if _, err = a.Store.DB.Exec(`DROP TRIGGER deny_words_edit`); err != nil {
		t.Fatal(err)
	}
	request.Revision, request.CustomWords = sub.Revision, subscriptionWordsRaw(t, "valid retry => second")
	updated, err := a.saveSubscription(ctx, sub.ID, request)
	if err != nil || updated.Revision != sub.Revision+1 {
		t.Fatal("failed transaction consumed the editable revision", err)
	}
	if text, err := a.subscriptionWords(ctx, sub.ID); err != nil || text != "valid retry => second" {
		t.Fatal("successful retry did not publish the replacement", err)
	}
}

func TestSubscriptionWordsPersistAcrossActualApplicationRestart(t *testing.T) {
	a := subscriptionTestApp(t)
	cookie := setupTestAdmin(t, a)
	request := subscriptionRequest(t)
	text := " \nsynthetic-restart-words => 留存\r\n\n\t"
	request.CustomWords = subscriptionWordsRaw(t, text)
	sub := subscriptionWordsAPIWrite(t, a, cookie, "", request, text)
	sealed := subscriptionWordsCiphertext(t, a, sub.ID)
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if current, err := reopened.subscriptionWords(context.Background(), sub.ID); err != nil || current != text || !bytes.Equal(subscriptionWordsCiphertext(t, reopened, sub.ID), sealed) {
		t.Fatal("restart lost or rewrote the encrypted raw words", err)
	}
	subscriptionWordsAPIRead(t, reopened, cookie, sub.ID, text)
	if response := callTest(t, reopened.Handler(), "GET", "/api/subscriptions", "", nil, ""); response.Code != http.StatusUnauthorized || strings.Contains(response.Body.String(), "synthetic-restart-words") {
		t.Fatal("restart made editor words public", response.Code)
	}
}
