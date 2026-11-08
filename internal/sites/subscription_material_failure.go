package sites

import (
	"errors"
	"net/http"
)

type subscriptionMaterialFailure struct{ cause error }

func (e *subscriptionMaterialFailure) Error() string { return e.cause.Error() }
func (e *subscriptionMaterialFailure) Unwrap() error { return e.cause }

func markSubscriptionMaterialFailure(err error) error {
	return &subscriptionMaterialFailure{cause: err}
}

// IsSubscriptionMaterialFailure identifies a completed material response that
// did not supply a torrent. Local admission and indeterminate transport errors
// carry their original errors and are not eligible for a resource cooldown.
func IsSubscriptionMaterialFailure(err error) bool {
	var failure *subscriptionMaterialFailure
	return errors.As(err, &failure)
}

func subscriptionMaterialHTTPFailure(status int) bool {
	return status >= 200 && status != http.StatusOK && status != http.StatusMovedPermanently && status != http.StatusFound
}

type siteHTTPResponseError struct {
	status  int
	message string
}

func (e *siteHTTPResponseError) Error() string { return e.message }
