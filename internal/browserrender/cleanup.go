package browserrender

import (
	"errors"
	"syscall"
	"time"
)

// Paired Playwright 1.62 removeFolders uses fs.rm(recursive, force,
// maxRetries:10). Node's default retryDelay is 100ms with linear backoff for
// these filesystem errors. Chromium may finish an atomic Preferences write
// after its parent exits, making a single recursive removal fail ENOTEMPTY.
// The remover parameter keeps the actual owned-file/error transition testable;
// production always uses os.RemoveAll on the one freshly created profile.
func removeBrowserProfile(path string, remove func(string) error) error {
	for attempt := 0; ; attempt++ {
		err := remove(path)
		if err == nil || attempt >= 10 || !browserProfileRetryable(err) {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * 100 * time.Millisecond)
	}
}

func browserProfileRetryable(err error) bool {
	return errors.Is(err, syscall.EBUSY) || errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE) || errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EPERM)
}

func browserProfileCleanupOutcome(primary, cleanup error) error {
	if cleanup == nil {
		return primary
	}
	return errors.Join(primary, &Error{Code: "browser_profile_cleanup_failed"})
}
