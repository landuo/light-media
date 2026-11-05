// Independently compiled actual SDK process evidence, not product or business code.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"light-media/internal/plugins"
)

func manifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "OwnedTimezoneProbe", Name: "Owned timezone SDK probe", Version: "1.0.0", Runtime: "go-executable", Entry: "timezone-sdk-probe", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"inspect", "stop"}}
}

func handle(_ context.Context, method string, _ json.RawMessage, _ plugins.HostCaller) (json.RawMessage, error) {
	if method == "stop" {
		return json.RawMessage(`null`), nil
	}
	value, present := os.LookupEnv("TZ")
	resolved := value
	if !present {
		// Fixed source core.config.TZ default; the caller need not query a secret.
		resolved = "Asia/Shanghai"
	}
	return json.Marshal(map[string]any{"environment_present": present, "environment_value": value, "source_resolved": resolved, "pid": os.Getpid()})
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--worker" {
		if err := plugins.ServeConcurrent(context.Background(), os.Stdin, os.Stdout, manifest(), handle); err != nil && !errors.Is(err, io.EOF) {
			os.Exit(2)
		}
		return
	}
	executable, err := os.Executable()
	if err != nil {
		panic(err)
	}
	rows := []map[string]any{}
	for _, sample := range []struct{ Present bool; Value string }{{false, ""}, {true, "Asia/Shanghai"}, {true, "Pacific/Auckland"}} {
		if sample.Present {
			err = os.Setenv("TZ", sample.Value)
		} else {
			err = os.Unsetenv("TZ")
		}
		if err != nil {
			panic(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		client, err := (plugins.Runner{ConcurrentCalls: true, Timeout: 5 * time.Second}).Start(ctx, plugins.Command{Path: executable, Args: []string{"--worker"}}, manifest(), plugins.SessionBinding{Identity: plugins.Identity{PluginID: manifest().PluginID, InstanceID: manifest().PluginID, Version: manifest().Version}, Generation: 1})
		if err != nil {
			panic(err)
		}
		result, err := client.Call(ctx, "inspect", json.RawMessage(`{}`), nil)
		if err != nil {
			panic(err)
		}
		var observed map[string]any
		if err = json.Unmarshal(result.Result, &observed); err != nil {
			panic(err)
		}
		if observed["environment_present"] != sample.Present || observed["environment_value"] != sample.Value || observed["pid"] == float64(os.Getpid()) {
			panic("actual worker did not inherit exact host TZ environment")
		}
		expected := sample.Value
		if !sample.Present { expected = "Asia/Shanghai" }
		if observed["source_resolved"] != expected { panic("source default mismatch") }
		if err = client.Stop(ctx); err != nil {
			panic(err)
		}
		cancel()
		observed["separate_sdk_process"] = true
		observed["clean_stop_passed"] = true
		rows = append(rows, observed)
	}
	if err = json.NewEncoder(os.Stdout).Encode(map[string]any{"status": "passed", "classification": "Actual current separately executing SDK worker; host TZ inherited exactly and source constant default applied by ordinary Go plugin code. Not a Linux-host/UI acceptance claim.", "cases": rows}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
