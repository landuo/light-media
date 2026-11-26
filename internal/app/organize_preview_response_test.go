package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestInteractivePreviewResponseWhileAuditWriterBusy(t *testing.T) {
	a := newTestApp(t)
	tx, err := a.Store.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	handler := a.requestLogged(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		audit := r.Context().Value(organizeAuditKey{}).(*organizeRequestAudit)
		audit.displayPreview = true
		writeJSON(w, r, map[string]string{"target": "/library/verified.mkv"})
	}))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		handler.ServeHTTP(w, r)
	}))
	defer srv.Close()
	defer tx.Rollback()
	client := srv.Client()
	client.Timeout = 2 * time.Second
	res, err := client.Post(srv.URL+"/api/files/organize", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	var out struct{ Data map[string]string }
	if err != nil || int64(len(body)) != res.ContentLength || json.Unmarshal(body, &out) != nil || out.Data["target"] != "/library/verified.mkv" {
		t.Fatalf("incomplete display preview: status=%d, length=%d, error=%v", res.StatusCode, res.ContentLength, err)
	}
	select {
	case <-done:
		t.Fatal("handler dropped the audit while the writer was occupied")
	default:
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("audit did not finish after releasing writer")
	}
	var count int
	err = a.Store.DB.QueryRow("SELECT COUNT(*) FROM audit_log WHERE json_extract(payload,'$.request_id')=?", res.Header.Get("X-Request-ID")).Scan(&count)
	if err != nil || count != 1 {
		t.Fatalf("audit count=%d error=%v", count, err)
	}
}
