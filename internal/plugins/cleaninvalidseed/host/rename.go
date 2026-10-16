package seedhost

func renameNoReplace(from int, old string, to int, name string) error {
	if from != to {
		return ErrUnsafe
	}
	return renameExclusive(from, old, name)
}
