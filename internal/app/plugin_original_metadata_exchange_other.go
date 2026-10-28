//go:build !linux && !darwin

package app

import "errors"

func originalMetadataExchange(parent int, left, right string) error {
	return errors.New("metadata atomic exchange is unavailable on this platform")
}
