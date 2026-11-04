package mediaservervendors

// Admit only the actual nonnil retained SDK Library. A host identity table may
// retain this pointer without exposing fields or manufacturing a truthy value.
func IsPlexRefreshLibraryObject(value any) bool {
	library, ok := value.(*plexSDKLibrary)
	return ok && library != nil
}
