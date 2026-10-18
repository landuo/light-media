package download

// mutationNotStarted is constructed only at a read-only/local boundary before
// issuing the selected daemon mutation. The seal and concrete type are private:
// neither daemon JSON nor an exported Error.Code can produce this proof.
type mutationNotStarted struct {
	cause error
	seal  *mutationPhaseSeal
}

type mutationPhaseSeal struct{ marker byte }

var mutationNotStartedSeal = &mutationPhaseSeal{marker: 1}

func (e *mutationNotStarted) Error() string { return e.cause.Error() }
func (e *mutationNotStarted) Unwrap() error { return e.cause }

func beforeMutation(err error) error {
	if err == nil {
		return nil
	}
	return &mutationNotStarted{cause: err, seal: mutationNotStartedSeal}
}

// MutationNotStarted proves that this Client call did not issue its selected
// mutation. Authentication/session and target lookup requests may have occurred.
// Only the exact returned error is accepted: arbitrary wrapping or joining with
// another failure loses the proof rather than certifying an unknown aggregate.
func MutationNotStarted(err error) bool {
	e, ok := err.(*mutationNotStarted)
	return ok && e != nil && e.cause != nil && e.seal == mutationNotStartedSeal
}
