// nasbench wraps one synthetic sqlitebench invocation and records the resource
// controls visible in the current container. It does not run a server.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const jsonLimit = 1 << 20

type boundedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - b.buf.Len()
	if len(p) > remaining {
		if remaining > 0 {
			b.buf.Write(p[:remaining])
		}
		return max(remaining, 0), errors.New("child output exceeded capture limit")
	}
	return b.buf.Write(p)
}

type reading struct {
	Value       string `json:"value,omitempty"`
	Unavailable string `json:"unavailable,omitempty"`
}

type childUsage struct {
	Available       bool   `json:"available"`
	MaximumRSSBytes int64  `json:"maximum_rss_bytes,omitempty"`
	Scope           string `json:"scope"`
}

type report struct {
	SchemaVersion     string             `json:"schema_version"`
	Status            string             `json:"status"`
	Runtime           string             `json:"runtime"`
	Evidence          string             `json:"evidence"`
	Platform          string             `json:"platform"`
	GoVersion         string             `json:"go_version"`
	GOMEMLIMIT        string             `json:"gomemlimit,omitempty"`
	Benchmark         json.RawMessage    `json:"benchmark,omitempty"`
	ChildUsage        childUsage         `json:"child_usage"`
	CgroupBefore      map[string]reading `json:"cgroup_before"`
	CgroupAfter       map[string]reading `json:"cgroup_after"`
	WrapperSelfStatus map[string]reading `json:"wrapper_self_status"`
	ElapsedMS         float64            `json:"elapsed_ms"`
	Error             string             `json:"error,omitempty"`
	Limitations       []string           `json:"limitations"`
}

func readSmall(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil {
		return "", err
	}
	if len(b) > 16384 {
		return "", errors.New("resource file exceeded read limit")
	}
	return strings.TrimSpace(string(b)), nil
}

func cgroupSnapshot() map[string]reading {
	result := map[string]reading{}
	for _, name := range []string{"memory.max", "memory.peak", "memory.current", "cpu.max"} {
		value, err := readSmall(filepath.Join("/sys/fs/cgroup", name))
		if err != nil {
			result[name] = reading{Unavailable: "not_readable_in_current_environment"}
		} else {
			result[name] = reading{Value: value}
		}
	}
	return result
}

func selfStatus() map[string]reading {
	result := map[string]reading{"scope": {Value: "wrapper_only_not_sqlitebench_or_process_tree"}}
	value, err := readSmall("/proc/self/status")
	if err != nil {
		result["proc_self_status"] = reading{Unavailable: "not_readable_in_current_environment"}
		return result
	}
	for _, line := range strings.Split(value, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch key {
		case "VmRSS", "VmHWM", "VmSize", "Threads":
			result[key] = reading{Value: strings.TrimSpace(val)}
		}
	}
	return result
}

func parseBenchmark(b []byte) (json.RawMessage, error) {
	if len(b) > jsonLimit {
		return nil, errors.New("benchmark JSON exceeded 1 MiB")
	}
	var header struct {
		Status        string `json:"status"`
		Evidence      string `json:"evidence"`
		Driver        string `json:"driver"`
		SQLiteVersion string `json:"sqlite_version"`
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	if err := decoder.Decode(&header); err != nil {
		return nil, errors.New("child did not return valid benchmark JSON")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("child returned trailing data after benchmark JSON")
	}
	if header.Status != "passed" || header.Evidence != "synthetic_local_filesystem" || header.Driver != "modernc" || header.SQLiteVersion == "" {
		return nil, errors.New("benchmark did not report a passed modernc synthetic run")
	}
	return json.RawMessage(bytes.TrimSpace(b)), nil
}

func run(ctx context.Context, benchPath string) (r report, returnedErr error) {
	start := time.Now()
	r = report{SchemaVersion: "m0-nasbench/v1", Status: "failed", Runtime: "single_synthetic_run", Evidence: "single_synthetic_database_run_with_observed_environment", Platform: runtime.GOOS + "/" + runtime.GOARCH, GoVersion: runtime.Version(), GOMEMLIMIT: os.Getenv("GOMEMLIMIT"), CgroupBefore: cgroupSnapshot(), Limitations: []string{
		"one_synthetic_sqlite_workload_not_production_application_or_idle_memory_acceptance",
		"cgroup_memory_peak_covers_current_cgroup_members_not_only_sqlitebench",
		"child_maxrss_is_wait4_maximum_for_the_child_and_waited_descendants_not_summed_process_tree_memory",
		"wrapper_proc_self_status_is_wrapper_only",
		"does_not_prove_power_loss_fsync_or_72_hour_stability",
	}}
	defer func() {
		r.ElapsedMS = float64(time.Since(start).Microseconds()) / 1000
		r.CgroupAfter = cgroupSnapshot()
		r.WrapperSelfStatus = selfStatus()
		if returnedErr != nil {
			r.Error = returnedErr.Error()
		}
	}()
	stdout := boundedBuffer{limit: jsonLimit}
	stderr := boundedBuffer{limit: 8192}
	cmd := exec.CommandContext(ctx, benchPath, "--driver", "modernc", "--temp-parent", "/data", "--timeout", "90s")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	r.ChildUsage = processUsage(cmd.ProcessState)
	if err != nil {
		if ctx.Err() != nil {
			return r, errors.New("benchmark process exceeded wrapper deadline or was cancelled")
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return r, fmt.Errorf("benchmark process failed with exit code %d", exitErr.ExitCode())
		}
		return r, errors.New("benchmark could not start or child output capture failed")
	}
	r.Benchmark, err = parseBenchmark(stdout.buf.Bytes())
	if err != nil {
		return r, err
	}
	r.Status = "passed"
	return r, nil
}

func siblingBenchmark() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	for _, name := range []string{"sqlitebench", "sqlitebench-linux-amd64"} {
		path := filepath.Join(filepath.Dir(self), name)
		if stat, e := os.Stat(path); e == nil && !stat.IsDir() {
			return path, nil
		}
	}
	return "", errors.New("sibling sqlitebench binary not found; provide -bench PATH")
}

func main() {
	benchPath := flag.String("bench", "", "sqlitebench path; default searches beside this executable")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected arguments")
		os.Exit(2)
	}
	path := *benchPath
	if path == "" {
		var err error
		path, err = siblingBenchmark()
		if err != nil {
			json.NewEncoder(os.Stdout).Encode(map[string]any{"schema_version": "m0-nasbench/v1", "status": "failed", "runtime": "single_synthetic_run", "error": err.Error()})
			os.Exit(1)
		}
	}
	// A small outer allowance permits child exit/reaping after its own 90s limit.
	ctx, cancel := context.WithTimeout(context.Background(), 95*time.Second)
	defer cancel()
	r, err := run(ctx, path)
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if e := encoder.Encode(r); e != nil {
		fmt.Fprintln(os.Stderr, "report encoding failed: "+strconv.Quote(e.Error()))
		os.Exit(1)
	}
	if err != nil {
		os.Exit(1)
	}
}
