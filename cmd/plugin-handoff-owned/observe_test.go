package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestStoppedObservationOnlyAcceptsStoppedOriginalContainer(t *testing.T) {
	fields := map[string]any{"container_id": sourceContainer, "image_sha256": "sha256:" + strings.Repeat("a", 64), "finished_at": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), "restart_policy": "no", "running": false, "restarting": false, "pid": 0}
	raw, _ := json.Marshal(fields)
	if _, e := stoppedObservation(raw); e != nil {
		t.Fatal(e)
	}
	for _, field := range []string{"running", "restarting", "pid", "container_id", "restart_policy"} {
		v := map[string]any{}
		for k, x := range fields {
			v[k] = x
		}
		switch field {
		case "running", "restarting":
			v[field] = true
		case "pid":
			v[field] = 100
		case "container_id":
			v[field] = strings.Repeat("b", 64)
		case "restart_policy":
			v[field] = "always"
		}
		raw, _ := json.Marshal(v)
		if _, e := stoppedObservation(raw); e == nil {
			t.Fatal("non-quiescent source accepted", field)
		}
	}
}
