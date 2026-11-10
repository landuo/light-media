package app

import (
	"context"
	"testing"
)

func TestTransferRepeatResultRequiresCompletedJob(t *testing.T) {
	a, source, target := importTestApp(t)
	_, job := historyParityPlan(t, a, source, target, "repeat", "Repeat", "succeeded", "already_imported", "repeat.mkv")
	for _, sample := range []struct{ state, want string }{
		{"succeeded", "success"}, {"running", "running"}, {"queued", "queued"}, {"failed", "failed"},
	} {
		job.State = sample.state
		out, err := a.originalTransferJobResult(context.Background(), job)
		if err != nil || out == nil || len(out.Items) != 1 || out.Items[0].Status != sample.want {
			t.Fatalf("%s repeat result: %+v, %v", sample.state, out, err)
		}
		if sample.state == "succeeded" && (out.Failed != 0 || out.Items[0].Error != "") {
			t.Fatalf("completed repeat was reported as failed: %+v", out)
		}
	}
}
