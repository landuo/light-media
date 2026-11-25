package removelink

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	plugins "light-media/builtinplugins/internal/sdk"
)

func TestSource312CustomExtensionAssignmentPrecedesDelayOverflow(t *testing.T) {
	raw, err := os.ReadFile("reference/log-repr-source312.json")
	if err != nil {
		t.Fatal(err)
	}
	var suite struct {
		Partial struct {
			Source string `json:"source_sha256"`
			Cases  []struct {
				Name, Custom string
				RawDelay     string   `json:"raw_delay_json"`
				Parsed       []string `json:"parsed_extensions"`
				Delay        int
			}
		} `json:"partial_config"`
	}
	if json.Unmarshal(raw, &suite) != nil || suite.Partial.Source != "22fb375e531378b3454607c7bca305b637abd60ee8ed69e14e32fbc2ddfc5485" || len(suite.Partial.Cases) != 3 {
		t.Fatal("unattested source partial assignment fixture")
	}
	for _, sample := range suite.Partial.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			p := NewPlugin()
			p.machine.Config.Delay = 77
			p.machine.Config.Extensions = []string{".old"}
			p.machine.Config.Enabled = true
			queued := &Task{Path: "owned-prior-task"}
			p.machine.Queue = []*Task{queued}
			due := time.Unix(100, 0)
			p.machine.TimerDue = &due
			p.machine.Watches = []Watch{{Path: "owned-prior-watch", Mode: "hardlink"}}
			watch := newWatchRuntime(9)
			p.watch = watch
			defer watch.abort()
			logs := 0
			host := func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				var message struct{ Level, Message string }
				if method != plugins.LogWriteCapability || json.Unmarshal(raw, &message) != nil || message.Level != "INFO" || message.Message != "初始化媒体文件清理插件" {
					t.Fatalf("unexpected pre-overflow effect: %s %s", method, raw)
				}
				logs++
				return json.RawMessage(`null`), nil
			}
			custom, _ := json.Marshal(sample.Custom)
			input := json.RawMessage(`{"enabled":false,"custom_scrap_extensions":` + string(custom) + `,"delay_seconds":` + sample.RawDelay + `}`)
			_, err := p.Handle(context.Background(), "init", input, host)
			if !errors.Is(err, errDelayOverflow) || !reflect.DeepEqual(p.machine.Config.Extensions, sample.Parsed) || p.machine.Config.Custom != sample.Custom || p.machine.Config.Delay != sample.Delay {
				t.Fatalf("source partial assignment: extensions=%v custom=%q delay=%d err=%v; source=%+v", p.machine.Config.Extensions, p.machine.Config.Custom, p.machine.Config.Delay, err, sample)
			}
			if logs != 1 || p.watch != watch || watch.closed || len(p.machine.Watches) != 1 || len(p.machine.Queue) != 1 || p.machine.Queue[0] != queued || queued.Processed || p.machine.TimerDue != &due || p.machine.StateValue() != false {
				t.Fatal("overflow lost earlier assignments or stopped/flushed prior runtime")
			}
		})
	}
}

// Expected conversion, partial assignment and pre-stop behavior were observed
// from the installed 2.18 init_plugin in the immutable Python3.12 image.
func TestSource312DelayOverflowBeforeStop(t *testing.T) {
	raw, err := os.ReadFile("reference/init-source312-overflow.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		SourceSHA string `json:"source_sha256"`
		Cases     []struct {
			Name     string `json:"name"`
			RawDelay string `json:"raw_delay_json"`
			Error    *struct {
				Class string `json:"class"`
			} `json:"error"`
			Delay int `json:"delay"`
		} `json:"cases"`
	}
	if json.Unmarshal(raw, &oracle) != nil || oracle.SourceSHA != "22fb375e531378b3454607c7bca305b637abd60ee8ed69e14e32fbc2ddfc5485" || len(oracle.Cases) != 8 {
		t.Fatal("fixed actual source oracle")
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			config := NewConfig()
			config.Delay = 77
			input := json.RawMessage(`{"enabled":false,"delay_seconds":` + sample.RawDelay + `}`)
			err := config.Apply(input)
			if (err != nil) != (sample.Error != nil) || config.Delay != sample.Delay {
				t.Fatalf("source conversion/old delay: error=%v delay=%d expected=%+v", err, config.Delay, sample)
			}
			if sample.Error == nil {
				return
			}
			if sample.Error.Class != "OverflowError" || !errors.Is(err, errDelayOverflow) {
				t.Fatal("source exception class", err)
			}
			p := NewPlugin()
			p.machine.Config.Delay = 77
			p.machine.Config.Enabled = true
			queued := &Task{Path: "owned-prior-task"}
			p.machine.Queue = []*Task{queued}
			due := time.Unix(100, 0)
			p.machine.TimerDue = &due
			p.machine.Watches = []Watch{{Path: "owned-prior-watch", Mode: "hardlink"}}
			watch := newWatchRuntime(9)
			p.watch = watch
			logs := 0
			host := func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				var message struct{ Level, Message string }
				if method != plugins.LogWriteCapability || json.Unmarshal(raw, &message) != nil || message.Level != "INFO" || message.Message != "初始化媒体文件清理插件" {
					t.Fatalf("pre-overflow effect is not the single source initialization log: %s %s", method, raw)
				}
				logs++
				return json.RawMessage(`null`), nil
			}
			if _, err = p.Handle(context.Background(), "init", input, host); !errors.Is(err, errDelayOverflow) {
				t.Fatal("worker did not propagate pre-stop source overflow", err)
			}
			if logs != 1 {
				t.Fatal("source initialization INFO count", logs)
			}
			if p.watch != watch || len(p.machine.Queue) != 1 || p.machine.Queue[0] != queued || queued.Processed || p.machine.TimerDue != &due || len(p.machine.Watches) != 1 || p.machine.Config.Delay != 77 || p.machine.StateValue() != false {
				t.Fatal("failed config stopped/flushed old observer, queue or timer, or lost source partial state")
			}
		})
	}
}
