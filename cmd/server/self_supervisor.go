package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

const selfControlGreeting = "light-media/self-service/v1"

type selfServiceConfiguration struct {
	Listen  string
	DataDir string
	Safe    bool
}

type selfServiceStatus struct {
	ready  bool
	pid    int
	closed string
	err    error
}

func parseSelfServiceStatus(line string) selfServiceStatus {
	fields := strings.Fields(line)
	if len(fields) != 2 {
		return selfServiceStatus{err: errors.New("invalid_service_status")}
	}
	if fields[0] == "ready" {
		pid, err := strconv.Atoi(fields[1])
		if err != nil || pid <= 0 {
			return selfServiceStatus{err: errors.New("invalid_service_pid")}
		}
		return selfServiceStatus{ready: true, pid: pid}
	}
	if fields[0] == "closed" && (fields[1] == "restart" || fields[1] == "stop" || fields[1] == "error") {
		return selfServiceStatus{closed: fields[1]}
	}
	return selfServiceStatus{err: errors.New("invalid_service_status")}
}

// This is bounded control-protocol input, not a business-material limit.
func readSelfServiceStatuses(reader io.Reader, events chan<- selfServiceStatus) {
	defer close(events)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 128), 256)
	count := 0
	for scanner.Scan() {
		count++
		if count > 2 {
			events <- selfServiceStatus{err: errors.New("too_many_service_statuses")}
			return
		}
		status := parseSelfServiceStatus(scanner.Text())
		events <- status
		if status.err != nil {
			return
		}
	}
	if scanner.Err() != nil {
		events <- selfServiceStatus{err: errors.New("service_status_read_failed")}
	}
}

type selfServiceObservation struct {
	ready  bool
	closed string
	err    error
}

func (s *selfServiceObservation) accept(event selfServiceStatus, childPID int) {
	if s.err != nil {
		return
	}
	if event.err != nil {
		s.err = event.err
		return
	}
	if event.ready {
		if s.ready || s.closed != "" || event.pid != childPID {
			s.err = errors.New("service_ready_identity_or_order_failed")
			return
		}
		s.ready = true
		return
	}
	if !s.ready || s.closed != "" {
		s.err = errors.New("service_closed_order_failed")
		return
	}
	s.closed = event.closed
}

func (s selfServiceObservation) next(parent context.Context, waitErr error) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	if waitErr != nil {
		return false, errors.New("service_child_exit_failed")
	}
	if parent.Err() != nil && s.closed != "error" {
		// A clean stop may beat the child's initial App/listener creation.
		return false, nil
	}
	if !s.ready || s.closed == "" || s.closed == "error" {
		return false, errors.New("service_generation_not_closed_successfully")
	}
	return s.closed == "restart", nil
}

func reportSelfGeneration(event string, generation uint64, childPID int, fields map[string]any) {
	if fields == nil {
		fields = map[string]any{}
	}
	fields["event"], fields["generation"] = event, generation
	fields["supervisor_pid"], fields["service_pid"] = os.Getpid(), childPID
	_ = json.NewEncoder(os.Stdout).Encode(fields)
}

func runSelfServerSupervisor(parent context.Context, config selfServiceConfiguration) error {
	// No caller-supplied program, shell, PID or executable path is accepted.
	executable, err := os.Executable()
	if err != nil {
		return errors.New("self_executable_unavailable")
	}
	for generation := uint64(1); ; generation++ {
		if parent.Err() != nil {
			return nil
		}
		restart, err := runSelfServerGeneration(parent, executable, config, generation)
		if err != nil {
			return err
		}
		if !restart || parent.Err() != nil {
			return nil
		}
	}
}

// One direct child, one original Start/Wait handle. This function never uses
// CommandContext's force-kill cancellation, FindProcess, PID scans or signals.
// Closing its owned control pipe requests child shutdown; Wait has no invented
// timeout/kill fallback. A pending old child cannot produce a new generation.
func runSelfServerGeneration(parent context.Context, executable string, config selfServiceConfiguration, generation uint64) (bool, error) {
	controlRead, controlWrite, err := os.Pipe()
	if err != nil {
		return false, err
	}
	defer controlRead.Close()
	defer controlWrite.Close()
	statusRead, statusWrite, err := os.Pipe()
	if err != nil {
		return false, err
	}
	defer statusRead.Close()
	defer statusWrite.Close()
	if parent.Err() != nil {
		return false, nil
	}
	command := exec.Command(executable, "-listen", config.Listen, "-data-dir", config.DataDir, "-safe="+strconv.FormatBool(config.Safe), "-internal-service-child")
	command.ExtraFiles = []*os.File{controlRead, statusWrite}
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err = command.Start(); err != nil {
		return false, errors.New("service_child_start_failed")
	}
	// Only the service child owns these ends; never leak them to later children.
	_ = controlRead.Close()
	_ = statusWrite.Close()
	childPID := command.Process.Pid
	reportSelfGeneration("service_generation_started", generation, childPID, nil)
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	events := make(chan selfServiceStatus, 4)
	go readSelfServiceStatuses(statusRead, events)
	observation := selfServiceObservation{}
	if _, err = fmt.Fprintln(controlWrite, selfControlGreeting); err != nil {
		observation.err = errors.New("service_control_handshake_failed")
		_ = controlWrite.Close()
	}
	stop := parent.Done()
	consume := func(event selfServiceStatus) {
		wasReady := observation.ready
		observation.accept(event, childPID)
		if observation.err != nil {
			_ = controlWrite.Close()
		}
		if !wasReady && observation.ready {
			reportSelfGeneration("service_generation_ready", generation, childPID, nil)
		}
	}
	var waitErr error
	complete := false
	for !complete {
		select {
		case <-stop:
			_ = controlWrite.Close()
			stop = nil
		case event, open := <-events:
			if !open {
				events = nil
			} else {
				consume(event)
			}
		case waitErr = <-waited:
			complete = true
		}
	}
	_ = controlWrite.Close()
	// Child marks both inherited FDs close-on-exec, and closes its own copies.
	// After actual Wait there can be no legitimate later status producer.
	if events != nil {
		for event := range events {
			consume(event)
		}
	}
	reportSelfGeneration("service_generation_exited", generation, childPID, map[string]any{"wait_success": waitErr == nil, "ready": observation.ready, "close_reason": observation.closed})
	return observation.next(parent, waitErr)
}
