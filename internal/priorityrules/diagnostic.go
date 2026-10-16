package priorityrules

import "errors"

// Diagnostic identifies the failing rule without carrying its expression,
// regex, resource title or private site data. Error preserves oracle semantics.
type Diagnostic struct {
	Group string
	Tier  int
	Rule  string
	Cause error
}

func (d *Diagnostic) Error() string { return d.Cause.Error() }
func (d *Diagnostic) Unwrap() error { return d.Cause }
func ruleFailure(err error, rule string) error {
	if err == nil {
		return nil
	}
	return &Diagnostic{Rule: rule, Cause: err}
}
func groupFailure(err error, group string, tier int) error {
	if err == nil {
		return nil
	}
	d := &Diagnostic{Group: group, Tier: tier, Cause: err}
	var inner *Diagnostic
	if errors.As(err, &inner) {
		d.Rule, d.Cause = inner.Rule, inner.Cause
	}
	return d
}
