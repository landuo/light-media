package browserrender

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func nativeDisplay(ctx context.Context) (string, func(), error) {
	if runtime.GOOS != "linux" || os.Getenv("DISPLAY") != "" {
		return "", func() {}, nil
	}
	path, err := exec.LookPath("Xvfb")
	if err != nil {
		return "", nil, &Error{Code: "browser_display_unavailable"}
	}
	read, write, err := os.Pipe()
	if err != nil {
		return "", nil, &Error{Code: "browser_display_failed"}
	}
	command := exec.CommandContext(ctx, path, "-displayfd", "3", "-screen", "0", "1024x768x24", "-nolisten", "tcp", "-noreset")
	command.ExtraFiles = []*os.File{write}
	command.Stderr, command.Stdout = io.Discard, io.Discard
	if err = command.Start(); err != nil {
		read.Close()
		write.Close()
		return "", nil, &Error{Code: "browser_display_failed"}
	}
	write.Close()
	done := make(chan struct{})
	go func() { _ = command.Wait(); close(done) }()
	cleanup := func() { read.Close(); _ = command.Process.Kill(); <-done }
	type displayResult struct {
		line string
		err  error
	}
	ready := make(chan displayResult, 1)
	go func() { line, err := bufio.NewReader(read).ReadString('\n'); ready <- displayResult{line, err} }()
	var line string
	select {
	case result := <-ready:
		line, err = result.line, result.err
	case <-ctx.Done():
		err = ctx.Err()
	// Paired PyVirtualDisplay 3.0 waits up to600s for displayfd readiness;
	// a caller deadline or an exited Xvfb still interrupts immediately.
	case <-time.After(600 * time.Second):
		err = context.DeadlineExceeded
	}
	read.Close()
	if err != nil {
		cleanup()
		return "", nil, &Error{Code: "browser_display_failed"}
	}
	number, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || number < 0 {
		cleanup()
		return "", nil, &Error{Code: "browser_display_failed"}
	}
	return ":" + strconv.Itoa(number), cleanup, nil
}
