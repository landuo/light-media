package catalog

// Classification consumes source metadata, never the translated display title.
// A successful detail fetch marks the metadata refreshed even when TMDB leaves
// fields empty; omitted fields are not inferred or made prerequisites.
func (c *Client) categoryMetadata(raw wireMedia, detail bool, result *Media) error {
	if len(raw.OriginCountry) > 100 || len(raw.ProductionCountries) > 100 || (raw.OriginalLanguage != "" && !twoLetters(raw.OriginalLanguage, 'a', 'z')) {
		return &Error{Code: "invalid_response"}
	}
	for _, code := range raw.OriginCountry {
		if !twoLetters(code, 'A', 'Z') {
			return &Error{Code: "invalid_response"}
		}
		result.OriginCountry = append(result.OriginCountry, code)
	}
	for _, country := range raw.ProductionCountries {
		if !twoLetters(country.ISO3166_1, 'A', 'Z') {
			return &Error{Code: "invalid_response"}
		}
		result.ProductionCountries = append(result.ProductionCountries, Country{ISO3166_1: country.ISO3166_1, Name: c.text(country.Name, 256)})
	}
	result.CategoryMetadataKnown = detail
	return nil
}

func twoLetters(value string, first, last byte) bool {
	return len(value) == 2 && value[0] >= first && value[0] <= last && value[1] >= first && value[1] <= last
}
