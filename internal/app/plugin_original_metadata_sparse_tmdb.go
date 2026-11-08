package app

import "fmt"

// The original TMDB scraper uses an empty season response when the recognized
// MediaInfo has no TMDB ID. Its fallback still writes the ordinary NFO shell.
func originalSparseTMDBMetadataNFO(fields map[string]any, season, episode any, imageDomain string) ([]byte, error) {
	if fields["type"] != "电影" && season != nil {
		if !sourceSubscriptionTruth(episode) {
			doc := newOriginalMetadataXML("season")
			doc.cdata("plot", "")
			doc.cdata("outline", "")
			doc.text("title", fmt.Sprintf("季 %v", season))
			for _, tag := range []string{"premiered", "releasedate", "year"} {
				doc.text(tag, "")
			}
			doc.text("seasonnumber", season)
			return doc.finish()
		}
		doc := newOriginalMetadataXML("episodedetails")
		doc.uniqueID("tmdb", "None")
		doc.text("tmdbid", "None")
		doc.text("title", fmt.Sprintf("第 %v 集", episode))
		doc.cdata("plot", "")
		doc.cdata("outline", "")
		doc.text("aired", "")
		doc.text("year", "")
		doc.text("season", season)
		doc.text("episode", episode)
		doc.text("rating", "0")
		return doc.finish()
	}
	root := "tvshow"
	if fields["type"] == "电影" {
		root = "movie"
	}
	doc := newOriginalMetadataXML(root)
	orEmpty := func(value any) any {
		if sourceSubscriptionTruth(value) {
			return value
		}
		return ""
	}
	doc.text("tmdbid", "")
	defaultTMDB := "true"
	if sourceSubscriptionTruth(fields["imdb_id"]) {
		defaultTMDB = "false"
	}
	doc.text("uniqueid", "", [2]string{"type", "tmdb"}, [2]string{"default", defaultTMDB})
	if id := fields["tvdb_id"]; sourceSubscriptionTruth(id) {
		doc.text("tvdbid", id)
		doc.text("uniqueid", id, [2]string{"type", "tvdb"})
	}
	if id := fields["imdb_id"]; sourceSubscriptionTruth(id) {
		doc.text("imdbid", id)
		doc.uniqueID("imdb", id)
	}
	doc.cdata("plot", orEmpty(fields["overview"]))
	doc.cdata("outline", orEmpty(fields["overview"]))
	for _, item := range sourceRows(fields["directors"]) {
		row := sourceMap(item)
		doc.text("director", orEmpty(row["name"]), [2]string{"tmdbid", fmt.Sprint(orEmpty(row["id"]))})
	}
	for _, item := range sourceRows(fields["actors"]) {
		row := sourceMap(item)
		doc.start("actor")
		doc.text("name", orEmpty(row["name"]))
		doc.text("type", "Actor")
		role := row["character"]
		if !sourceSubscriptionTruth(role) {
			role = row["role"]
		}
		doc.text("role", orEmpty(role))
		doc.text("tmdbid", orEmpty(row["id"]))
		if path := row["profile_path"]; sourceSubscriptionTruth(path) {
			if address, err := sourceImagePath(path, imageDomain); err == nil {
				doc.text("thumb", address)
			}
		}
		person := row["id"]
		if person == nil {
			person = "None"
		}
		doc.text("profile", fmt.Sprintf("https://www.themoviedb.org/person/%v", person))
		doc.end("actor")
	}
	for _, item := range sourceRows(fields["genres"]) {
		doc.text("genre", orEmpty(sourceMap(item)["name"]))
	}
	rating := fields["vote_average"]
	if !sourceSubscriptionTruth(rating) {
		rating = "0"
	}
	doc.text("rating", rating)
	if value := fields["content_rating"]; sourceSubscriptionTruth(value) {
		doc.text("mpaa", value)
	}
	for _, names := range []struct{ field, tag string }{{"production_companies", "studio"}, {"production_countries", "country"}} {
		for _, item := range sourceRows(fields[names.field]) {
			if name := sourceMap(item)["name"]; sourceSubscriptionTruth(name) {
				doc.text(names.tag, name)
			}
		}
	}
	if value := fields["runtime"]; sourceSubscriptionTruth(value) {
		doc.text("runtime", value)
	}
	doc.text("title", orEmpty(fields["title"]))
	doc.text("originaltitle", orEmpty(fields["original_title"]))
	doc.text("premiered", orEmpty(fields["release_date"]))
	doc.text("year", orEmpty(fields["year"]))
	if root == "tvshow" {
		doc.text("season", "-1")
		doc.text("episode", "-1")
	}
	return doc.finish()
}
