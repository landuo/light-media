//go:build !linux

package removelink

// Non-Linux source observer selection is excluded from the artifact contract.
// Polling permits owned local protocol tests on the development machine.
func sourceObserver(spec Watch) (observer, error) { return newPollingObserver(spec) }
