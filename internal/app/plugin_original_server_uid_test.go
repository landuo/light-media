package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
)

func TestOriginalPluginServerUIDMatchesPinnedHostAlgorithm(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-market/server-uid-oracle.json")
	var oracle struct {
		ServerDefault string `json:"server_default"`
		ShareDefault  bool   `json:"share_default"`
		CPU           []struct {
			Machine, Arch string
			UserAgent     string `json:"user_agent"`
		} `json:"cpu_cases"`
		Cases []struct {
			Kind          string
			Device, Inode uint64
			RootFailure   bool `json:"root_failure"`
			Node          *uint64
			UID           *string
			NodeCalled    bool `json:"node_called"`
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 6 {
		t.Fatal("fixed UID source oracle missing", err)
	}
	if originalPluginDefaultServerURL(func(string) (string, bool) { return "", false }) != oracle.ServerDefault || originalPluginStatisticShared(Settings{}) != oracle.ShareDefault {
		t.Fatal("fixed center defaults differ")
	}
	if originalPluginDefaultServerURL(func(string) (string, bool) { return "", true }) != "" || originalPluginDefaultServerURL(func(string) (string, bool) { return "http://owned.invalid/prefix", true }) != "http://owned.invalid/prefix" {
		t.Fatal("explicit center environment defaults differ")
	}
	if len(oracle.CPU) != 12 {
		t.Fatal("fixed CPU/User-Agent oracle missing")
	}
	for _, row := range oracle.CPU {
		if originalPluginServerCPUArch(row.Machine) != row.Arch || originalPluginServerIdentityAgent("Linux", "owned-test-kernel", row.Machine) != row.UserAgent {
			t.Fatal("fixed CPU/User-Agent algorithm differs", row.Machine)
		}
	}
	for _, row := range oracle.Cases {
		called := false
		actual := originalPluginUIDFromHost(func() (string, error) {
			if row.RootFailure {
				return "", errors.New("owned root failure")
			}
			return fmt.Sprintf("%d-%d", row.Device, row.Inode), nil
		}, func() (uint64, error) {
			called = true
			if row.Node == nil {
				return 0, errors.New("owned node failure")
			}
			return *row.Node, nil
		})
		want := ""
		if row.UID != nil {
			want = *row.UID
		}
		if actual != want || called != row.NodeCalled {
			t.Fatal("fixed filesystem/MAC algorithm differs", row.Kind)
		}
	}
	a := &App{}
	first, err := a.originalPluginServerUID(context.Background())
	if err != nil || len(first) != 64 {
		t.Fatal("native filesystem source identity unavailable")
	}
	second, err := a.originalPluginServerUID(context.Background())
	if err != nil || first != second {
		t.Fatal("same object did not cache successful source identity")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.originalPluginServerUID(cancelled); err == nil {
		t.Fatal("cancelled request used host identity")
	}
}
