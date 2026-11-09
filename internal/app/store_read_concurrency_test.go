package app

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"
)

func TestUIReadsContinueDuringOrganizeWrite(t *testing.T) {
	a, _, _ := importTestApp(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := a.Store.Put(ctx, "read_test", "main", map[string]int{"value": 1}); err != nil {
		t.Fatal(err)
	}
	job, err := a.enqueue(ctx, "read_test", map[string]string{"id": "read_test"}, "")
	if err != nil {
		t.Fatal(err)
	}
	history, err := a.transferHistoryReadTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = history.Commit(); err != nil {
		t.Fatal(err)
	}
	writer, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	if err = txPut(ctx, writer, "read_test", "main", map[string]int{"value": 2}); err != nil {
		t.Fatal(err)
	}
	var value map[string]int
	if err = a.Store.Get(ctx, "read_test", "main", &value); err != nil || value["value"] != 1 {
		t.Fatalf("reader must see committed state without waiting for writer: %v %v", value, err)
	}
	if _, err = a.jobQueue(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = a.jobAPI(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/jobs/"+job.ID, nil).WithContext(ctx), job.ID); err != nil {
		t.Fatal("single job progress waited for organizer:", err)
	}
	if _, err = a.transferHistory(ctx, httptest.NewRequest("GET", "/api/history/transfers", nil)); err != nil {
		t.Fatal(err)
	}
	a.status(ctx)
	if err = ctx.Err(); err != nil {
		t.Fatal("UI read blocked behind the organizer:", err)
	}
	if err = writer.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = a.Store.Get(ctx, "read_test", "main", &value); err != nil || value["value"] != 2 {
		t.Fatalf("reader missed committed change: %v %v", value, err)
	}
	// Every pooled connection is read-only, including connections created later.
	connections := make([]interface{ Close() error }, 0, 4)
	defer func() {
		for _, c := range connections {
			c.Close()
		}
	}()
	for i := 0; i < 4; i++ {
		c, err := a.Store.reader().Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, c)
		if _, err = c.ExecContext(ctx, `DELETE FROM records WHERE kind='read_test'`); err == nil {
			t.Fatal("read connection allowed a write")
		}
	}
}
