package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSelfServiceProtocolAndOriginalWaitDecision(t *testing.T) {
	for _, line := range []string{"", "ready 0", "ready other", "closed unknown", "ready 7 extra", "anything 7"} {
		if parseSelfServiceStatus(line).err == nil {
			t.Fatal("unbound protocol admitted", line)
		}
	}
	state := selfServiceObservation{}
	state.accept(parseSelfServiceStatus("ready 17"), 17)
	state.accept(parseSelfServiceStatus("closed restart"), 17)
	if next, err := state.next(context.Background(), nil); err != nil || !next {
		t.Fatal(next, err)
	}
	if next, err := state.next(context.Background(), errors.New("owned Wait failure")); err == nil || next {
		t.Fatal("failed Wait relaunched", next, err)
	}
	stopped, cancel := context.WithCancel(context.Background())
	cancel()
	if next, err := state.next(stopped, nil); err != nil || next {
		t.Fatal("stop lost to queued restart", next, err)
	}
	if next, err := (selfServiceObservation{}).next(stopped, nil); err != nil || next {
		t.Fatal("clean early stop relaunched or fabricated a failure", next, err)
	}
	for _, lines := range [][]string{{"closed restart"}, {"ready 18"}, {"ready 17", "ready 17"}, {"ready 17", "closed stop", "closed restart"}} {
		s := selfServiceObservation{}
		for _, line := range lines {
			s.accept(parseSelfServiceStatus(line), 17)
		}
		if next, err := s.next(context.Background(), nil); err == nil || next {
			t.Fatal("invalid identity/order relaunched", lines, next, err)
		}
	}
}

func TestSelfServiceStatusReaderBoundedAndClosed(t *testing.T) {
	for _, raw := range []string{"ready 17\nclosed stop\n", "ready 17\nclosed stop\nclosed restart\n", strings.Repeat("x", 300) + "\n"} {
		events := make(chan selfServiceStatus, 4)
		readSelfServiceStatuses(strings.NewReader(raw), events)
		count, failed := 0, false
		for event := range events {
			count++
			failed = failed || event.err != nil
		}
		if raw == "ready 17\nclosed stop\n" {
			if count != 2 || failed {
				t.Fatal(count, failed)
			}
		} else if !failed {
			t.Fatal("protocol bound not enforced")
		}
	}
}
