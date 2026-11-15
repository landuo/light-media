package app

// The trusted startup snapshot remains immutable. An explicit persisted user
// preference is read from the same atomic settings snapshot as other controls;
// legacy nil keeps RECOGNIZE_SOURCE's existing startup semantics.
func (a *App) effectiveRecognitionSource() string {
	if source := a.Settings().RecognitionSource; source != nil {
		return *source
	}
	if a.originalHostMediaRecognition != nil {
		return a.originalHostMediaRecognition.Source
	}
	return "themoviedb"
}
func validateRecognitionSource(source *string) error {
	if source == nil {
		return nil
	}
	switch *source {
	case "themoviedb", "douban", "bangumi", "anilist":
		return nil
	}
	return bad("INVALID_RECOGNIZE_SOURCE", "请选择已实现的媒体识别来源")
}
