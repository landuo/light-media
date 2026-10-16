package larkmessager

// sourceFailure carries a failed Python-style dynamic conversion to the nearest
// public error-returning API. It never turns an unsupported value into empty
// metadata or an approximate fmt representation.
type sourceFailure struct{ err error }

func sourceErrors(err *error) {
	if v := recover(); v != nil {
		if f, ok := v.(sourceFailure); ok {
			*err = f.err
			return
		}
		panic(v)
	}
}
