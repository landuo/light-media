package app

import (
	"encoding/json"
	"strings"

	"light-media/internal/notice"
	"light-media/internal/sourcejson"
)

// MoviePilot's DomUtils distinguishes an absent text node from an empty one.
// Match its minidom output, including CDATA, ordered attributes and UTF-8 header.
type originalMetadataXML struct {
	body  strings.Builder
	root  string
	depth int
	err   error
}

var originalMetadataXMLEscape = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")

func newOriginalMetadataXML(root string) *originalMetadataXML {
	doc := &originalMetadataXML{root: root}
	doc.body.WriteString("<?xml version=\"1.0\" encoding=\"utf-8\"?>\n")
	doc.start(root)
	return doc
}

func (doc *originalMetadataXML) start(tag string) {
	doc.body.WriteString(strings.Repeat("  ", doc.depth) + "<" + tag + ">\n")
	doc.depth++
}

func (doc *originalMetadataXML) end(tag string) {
	doc.depth--
	doc.body.WriteString(strings.Repeat("  ", doc.depth) + "</" + tag + ">\n")
}

func (doc *originalMetadataXML) stringify(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	raw, err := json.Marshal(value)
	if err != nil {
		doc.err = err
		return ""
	}
	text, err := sourcejson.String(raw)
	if err != nil {
		doc.err = err
	}
	return text
}

func (doc *originalMetadataXML) text(tag string, value any, attrs ...[2]string) {
	doc.body.WriteString(strings.Repeat("  ", doc.depth) + "<" + tag)
	for _, attr := range attrs {
		doc.body.WriteString(" " + attr[0] + `="` + originalMetadataXMLEscape.Replace(attr[1]) + `"`)
	}
	if value == nil {
		doc.body.WriteString("/>\n")
		return
	}
	doc.body.WriteString(">" + originalMetadataXMLEscape.Replace(doc.stringify(value)) + "</" + tag + ">\n")
}

func (doc *originalMetadataXML) cdata(tag string, value any) {
	text, ok := value.(string)
	if !ok {
		doc.err = notice.SourceError{Class: "TypeError"}
		return
	}
	if strings.Contains(text, "]]>") {
		doc.err = notice.SourceError{Class: "ValueError"}
		return
	}
	doc.body.WriteString(strings.Repeat("  ", doc.depth) + "<" + tag + "><![CDATA[" + text + "]]></" + tag + ">\n")
}

func (doc *originalMetadataXML) uniqueID(kind string, value any) {
	doc.text("uniqueid", value, [2]string{"type", kind}, [2]string{"default", "true"})
}

func (doc *originalMetadataXML) finish() ([]byte, error) {
	if doc.err != nil {
		return nil, doc.err
	}
	doc.end(doc.root)
	return []byte(doc.body.String()), nil
}

func originalMetadataValue(values ...any) any {
	for _, value := range values[:len(values)-1] {
		if sourceSubscriptionTruth(value) {
			return value
		}
	}
	return values[len(values)-1]
}

func originalProviderMetadataNFO(fields map[string]any, scraper string, season, episode any) ([]byte, error) {
	switch scraper {
	case "douban":
		return originalDoubanMetadataNFO(fields, season)
	case "anilist", "bangumi":
		if len(fields) == 0 {
			return nil, nil
		}
	default:
		return nil, nil
	}
	root := "tvshow"
	switch {
	case fields["type"] == "电影":
		root = "movie"
	case season != nil && episode != nil:
		root = "episodedetails"
	case season != nil:
		root = "season"
	}
	doc := newOriginalMetadataXML(root)
	appendOriginalProviderMetadata(doc, fields)
	switch root {
	case "episodedetails":
		doc.text("season", season)
		doc.text("episode", episode)
		doc.text("showtitle", originalMetadataValue(fields["title"], ""))
	case "season":
		doc.text("seasonnumber", season)
	case "tvshow":
		doc.text("season", "-1")
		doc.text("episode", "-1")
	}
	return doc.finish()
}

func appendOriginalProviderMetadata(doc *originalMetadataXML, fields map[string]any) {
	doc.text("title", originalMetadataValue(fields["title"], ""))
	doc.text("originaltitle", originalMetadataValue(fields["original_title"], ""))
	doc.text("year", originalMetadataValue(fields["year"], ""))
	doc.text("premiered", originalMetadataValue(fields["release_date"], ""))
	doc.text("rating", originalMetadataValue(fields["vote_average"], "0"))
	doc.cdata("plot", originalMetadataValue(fields["overview"], ""))
	doc.cdata("outline", originalMetadataValue(fields["overview"], ""))
	source := sourceText(fields["source"])
	alias := map[string]string{"themoviedb": "tmdb_id", "douban": "douban_id", "bangumi": "bangumi_id", "anilist": "anilist_id"}[source]
	if id := fields[alias]; alias != "" && id != nil && doc.stringify(id) != "" {
		doc.uniqueID(source, id)
	}
	for _, list := range []struct{ field, tag string }{{"genres", "genre"}, {"production_companies", "studio"}, {"directors", "director"}} {
		for _, entry := range sourceRows(fields[list.field]) {
			var name any
			if row, ok := entry.(map[string]any); ok {
				name = row["name"]
			} else {
				name = doc.stringify(entry)
			}
			if sourceSubscriptionTruth(name) {
				doc.text(list.tag, name)
			}
		}
	}
	for _, entry := range sourceRows(fields["actors"]) {
		actor := sourceMap(entry)
		if actor == nil {
			continue
		}
		doc.start("actor")
		doc.text("name", originalMetadataValue(actor["name"], ""))
		doc.text("role", originalMetadataValue(actor["character"], actor["role"], ""))
		avatar := originalMetadataValue(actor["avatar"], actor["images"], map[string]any{})
		if images := sourceMap(avatar); images != nil {
			doc.text("thumb", originalMetadataValue(images["large"], images["medium"], images["normal"], ""))
		}
		doc.end("actor")
	}
}

func originalDoubanMetadataNFO(fields map[string]any, season any) ([]byte, error) {
	root := "tvshow"
	if fields["type"] == "电影" {
		root = "movie"
	} else if season != nil {
		root = "season"
	}
	doc := newOriginalMetadataXML(root)
	doc.cdata("plot", originalMetadataValue(fields["overview"], ""))
	doc.cdata("outline", originalMetadataValue(fields["overview"], ""))
	if root == "season" {
		date := sourceText(fields["release_date"])
		dateRunes := []rune(date)
		doc.text("title", "季 "+doc.stringify(season))
		doc.text("premiered", date)
		doc.text("releasedate", date)
		doc.text("year", string(dateRunes[:min(4, len(dateRunes))]))
		doc.text("seasonnumber", season)
		return doc.finish()
	}
	for _, entry := range sourceRows(fields["directors"]) {
		director := sourceMap(entry)
		if director == nil {
			return nil, notice.SourceError{Class: "AttributeError"}
		}
		doc.text("director", originalMetadataValue(director["name"], ""))
	}
	for _, entry := range sourceRows(fields["actors"]) {
		actor := sourceMap(entry)
		if actor == nil {
			return nil, notice.SourceError{Class: "AttributeError"}
		}
		images := sourceMap(actor["avatar"])
		if _, present := actor["avatar"]; present && images == nil {
			return nil, notice.SourceError{Class: "AttributeError"}
		}
		doc.start("actor")
		doc.text("name", originalMetadataValue(actor["name"], ""))
		doc.text("type", "Actor")
		doc.text("role", originalMetadataValue(actor["character"], actor["role"], ""))
		doc.text("thumb", images["normal"])
		doc.text("profile", actor["url"])
		doc.end("actor")
	}
	doc.text("rating", originalMetadataValue(fields["vote_average"], "0"))
	doc.text("title", originalMetadataValue(fields["title"], ""))
	doc.text("year", originalMetadataValue(fields["year"], ""))
	if root == "tvshow" {
		doc.text("season", "-1")
		doc.text("episode", "-1")
	}
	return doc.finish()
}
