//go:build !unix

package selfupdate

import "errors"

// Exec is unavailable on platforms without exec(2); the check still runs so the
// caller can log why the artifact was downloaded but not activated.
func Exec(string, []string, []string) error {
	return errors.New("self update requires a unix platform")
}
