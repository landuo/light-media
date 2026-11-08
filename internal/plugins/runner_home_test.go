package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"strings"
	"testing"
	"time"
)

func runnerHomeManifest() Manifest {
	return Manifest{SchemaVersion: 1, PluginID: "RunnerHomeFixture", Name: "Owned HOME fixture", Version: "1.0", Runtime: "go-executable", Entry: "home-fixture", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"invoke", "stop"}}
}

type runnerHomeView struct {
	Present  bool   `json:"present"`
	Home     string `json:"home"`
	Fallback string `json:"fallback"`
	Secret   bool   `json:"secret"`
}

func TestRunnerHomeWorkerFixture(t *testing.T) {
	if !strings.Contains(strings.Join(os.Args, " "), "runner-home-worker") {
		return
	}
	handle := func(_ context.Context, method string, _ json.RawMessage, _ HostCaller) (json.RawMessage, error) {
		if method == "stop" {
			return json.RawMessage(`null`), nil
		}
		home, present := os.LookupEnv("HOME")
		_, secret := os.LookupEnv("OWNED_PROVIDER_CREDENTIAL")
		view := runnerHomeView{Present: present, Home: home, Secret: secret}
		if !present {
			if u, e := user.Current(); e == nil {
				view.Fallback = u.HomeDir
			}
		}
		return json.Marshal(view)
	}
	if err := ServeConcurrent(context.Background(), os.Stdin, os.Stdout, runnerHomeManifest(), handle); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}
func TestRunnerHOMEOnlyFiniteMetadataBothStartupPaths(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := Command{Path: exe, Args: []string{"-test.run=^TestRunnerHomeWorkerFixture$", "--", "runner-home-worker"}}
	for _, mode := range []string{"set", "empty", "absent"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("HOME", "/owned/home-metadata")
			t.Setenv("OWNED_PROVIDER_CREDENTIAL", "synthetic-do-not-inherit")
			want := runnerHomeView{Present: true, Home: "/owned/home-metadata"}
			if mode == "empty" {
				t.Setenv("HOME", "")
				want.Home = ""
			}
			if mode == "absent" {
				if err := os.Unsetenv("HOME"); err != nil {
					t.Fatal(err)
				}
				want.Present = false
				want.Home = ""
				if u, e := user.Current(); e == nil {
					want.Fallback = u.HomeDir
				}
			}
			check := func(raw json.RawMessage) {
				var got runnerHomeView
				if json.Unmarshal(raw, &got) != nil || got != want {
					t.Fatalf("HOME whitelist/source fallback mismatch got=%+v want=%+v", got, want)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			defer cancel()
			manifest := runnerHomeManifest()
			runner := Runner{Timeout: 2 * time.Second, ConcurrentCalls: true, RequireConcurrent: true}
			out, err := runner.Run(ctx, cmd, manifest, "OwnedHome", "invoke", json.RawMessage(`{}`), nil)
			if err != nil {
				t.Fatal("Run", err)
			}
			check(out.Result)
			client, err := runner.Start(ctx, cmd, manifest, SessionBinding{Identity: Identity{PluginID: manifest.PluginID, InstanceID: "OwnedHome", Version: manifest.Version}, Generation: 1})
			if err != nil {
				t.Fatal("Start", err)
			}
			out, err = client.Call(ctx, "invoke", json.RawMessage(`{}`), nil)
			if err != nil {
				t.Fatal(err)
			}
			check(out.Result)
			if err := client.Stop(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}
