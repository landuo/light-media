package plugins

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// Retained from the independent review's actual serial/concurrent malformed-peer
// reproduction: rejecting one reply is insufficient if the next call still runs.
// Owned malformed protocol peer: does not advertise or negotiate detail.
func TestCallbackUnnegotiatedDetailWorker(t *testing.T) {
	if len(os.Args) < 2 || !strings.HasPrefix(os.Args[len(os.Args)-1], "--owned-unnegotiated") {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	enc := json.NewEncoder(os.Stdout)
	calls := 0
	for scanner.Scan() {
		var req Frame
		if json.Unmarshal(scanner.Bytes(), &req) != nil {
			os.Exit(2)
		}
		reply := Frame{Protocol: 1, ID: req.ID, Identity: req.Identity, Type: "result"}
		switch req.Method {
		case "handshake":
			m := fixtureManifest()
			concurrent := 0
			if strings.HasSuffix(os.Args[len(os.Args)-1], "-concurrent") {
				concurrent = 1
			}
			reply.Result, _ = json.Marshal(Handshake{ConcurrentSession: concurrent, Protocol: 1, HostAPI: HostAPIVersion, PluginID: m.PluginID, Version: m.Version, Methods: m.Methods, Capabilities: m.Capabilities, Transport: &transportAdvertisement{Version: 1}, PersistentSession: 1})
		case "transport.negotiate":
			reply.Result = req.Payload
		case "transport.session":
			reply.Result = json.RawMessage(`{"version":1}`)
		case "transport.concurrent":
			reply.Result = req.Payload
		case "invoke":
			calls++
			if calls == 1 {
				detail := "unnegotiated owned text"
				reply.Error = &ReplyError{Code: "OPERATION_FAILED", Detail: &detail}
			} else {
				reply.Result = json.RawMessage(`{"continued":true}`)
			}
		case "stop":
			reply.Result = json.RawMessage(`null`)
		default:
			os.Exit(3)
		}
		if enc.Encode(reply) != nil {
			os.Exit(4)
		}
		if req.Method == "stop" {
			os.Exit(0)
		}
	}
	os.Exit(0)
}

func TestCallbackUnnegotiatedDetailRetiresSession(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		t.Run(fmt.Sprint(concurrent), func(t *testing.T) {
			path, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			mode := "--owned-unnegotiated"
			if concurrent {
				mode += "-concurrent"
			}
			c, err := (Runner{ConcurrentCalls: concurrent}).Start(context.Background(), Command{Path: path, Args: []string{"-test.run=^TestCallbackUnnegotiatedDetailWorker$", "--", mode}}, fixtureManifest(), sessionBinding())
			if err != nil {
				t.Fatal(err)
			}
			defer c.Stop(context.Background())
			_, err = c.Call(context.Background(), "invoke", json.RawMessage(`{}`), nil)
			if codeOf(err) != "invalid_frame" {
				t.Fatalf("unnegotiated not rejected: %v", err)
			}
			out, err := c.Call(context.Background(), "invoke", json.RawMessage(`{}`), nil)
			if err == nil {
				t.Fatalf("protocol-invalid session reused: %s", out.Result)
			}
		})
	}
}
