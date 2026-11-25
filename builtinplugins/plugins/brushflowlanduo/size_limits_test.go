package brushflowlanduo

import (
	"context"
	"testing"
)

func TestCanAddSizeRejectsProjectedOverflow(t *testing.T) {
	limit := 100.0
	task := &SizeTask{Downloader: "qb", DiskSize: &limit}
	gb := 1024.0 * 1024 * 1024
	if ok, _, err := CanAddSize(context.Background(), task, 90*gb, 20*gb, nil, 0, nil, nil); err != nil || ok {
		t.Fatalf("expected projected overflow rejection, ok=%v err=%v", ok, err)
	}
	if ok, _, err := CanAddSize(context.Background(), task, 90*gb, 5*gb, nil, 0, nil, nil); err != nil || !ok {
		t.Fatalf("expected room for candidate, ok=%v err=%v", ok, err)
	}
}
