package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"

	"light-media/internal/app"
)

const sourceContainer = "cb82e3f550881334ef3814426016d08d909bccd88d3297522756ba31654d129a"

// Only selected Docker fields enter memory/output. Container environments,
// arguments, health logs, mounts and credentials are never requested.
const stoppedObservationTemplate = `{"container_id":{{json .Id}},"image_sha256":{{json .Image}},"finished_at":{{json .State.FinishedAt}},"restart_policy":{{json .HostConfig.RestartPolicy.Name}},"running":{{json .State.Running}},"restarting":{{json .State.Restarting}},"pid":{{json .State.Pid}}}`

func stoppedObservation(raw []byte) ([]byte, error) {
	var fields map[string]json.RawMessage
	if len(raw) > 4096 || json.Unmarshal(raw, &fields) != nil || len(fields) != 7 {
		return nil, errors.New("source_state_observation_failed")
	}
	var image string
	if json.Unmarshal(fields["image_sha256"], &image) != nil || !strings.HasPrefix(image, "sha256:") {
		return nil, errors.New("source_state_observation_failed")
	}
	fields["image_sha256"], _ = json.Marshal(strings.TrimPrefix(image, "sha256:"))
	fields["observed_at"], _ = json.Marshal(time.Now().UTC().Format(time.RFC3339Nano))
	out, e := json.Marshal(fields)
	if e != nil || app.ValidateReviewedPluginStoppedInput(out) != nil {
		return nil, errors.New("original_source_is_not_verifiably_stopped")
	}
	return out, nil
}
func observeOriginalSource() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	raw, e := exec.CommandContext(ctx, "sudo", "-n", "docker", "inspect", "--format", stoppedObservationTemplate, sourceContainer).Output()
	if e != nil {
		return errors.New("source_state_observation_failed")
	}
	out, e := stoppedObservation(raw)
	if e != nil {
		return e
	}
	_, e = os.Stdout.Write(append(out, '\n'))
	return e
}
