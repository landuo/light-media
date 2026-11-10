package app

import (
	"context"
	"testing"
	"time"
)

func TestSiteMessagePolicyReadsWhileWriterBusy(t *testing.T) {
	a, _, _ := importTestApp(t)
	tx, err := a.Store.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := a.sourceSiteMessagePolicy(ctx, a.Settings()); err != nil {
		t.Fatalf("settings read waited for unrelated writer: %v", err)
	}
}
