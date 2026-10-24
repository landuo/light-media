package app

import (
	"net/http"
)

type siteOutcomeRoundTrip func(*http.Request) (*http.Response, error)

func (f siteOutcomeRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
