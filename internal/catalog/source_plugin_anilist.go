package catalog

import (
	"context"
	"encoding/json"
	"net/http"
)

// Fixed AniListApi detail/search GraphQL: complete media fields, never Explore
// cards. Endpoints and operations are finite; host retains proxy/HTTP guards.
const sourceAniListDetailQuery = `query ($id: Int!) { Media(id: $id, type: ANIME) { 
        
        id
        idMal
        title { romaji english native }
        format
        status
        description(asHtml: false)
        startDate { year month day }
        endDate { year month day }
        season
        seasonYear
        episodes
        duration
        countryOfOrigin
        coverImage { extraLarge large }
        bannerImage
        genres
        synonyms
        averageScore
        popularity
        isAdult
        siteUrl
        studios(isMain: true) { nodes { name } }
    
        staff(perPage: 25, sort: [RELEVANCE]) {
          edges { role node { id name { full native } image { large } siteUrl } }
        }
        characters(perPage: 25, sort: [ROLE, RELEVANCE]) {
          edges {
            role
            node { id name { full native } image { large } siteUrl }
            voiceActors(language: JAPANESE, sort: [RELEVANCE]) {
              id
              name { full native alternative }
              image { large medium }
              siteUrl
            }
          }
        }
        externalLinks { site url type }
     } }`
const sourceAniListSearchQuery = `
            query ($search: String!, $count: Int!) {
              Page(page: 1, perPage: $count) {
                media(search: $search, type: ANIME, sort: SEARCH_MATCH) { 
        
        id
        idMal
        title { romaji english native }
        format
        status
        description(asHtml: false)
        startDate { year month day }
        endDate { year month day }
        season
        seasonYear
        episodes
        duration
        countryOfOrigin
        coverImage { extraLarge large }
        bannerImage
        genres
        synonyms
        averageScore
        popularity
        isAdult
        siteUrl
        studios(isMain: true) { nodes { name } }
    
        staff(perPage: 25, sort: [RELEVANCE]) {
          edges { role node { id name { full native } image { large } siteUrl } }
        }
        characters(perPage: 25, sort: [ROLE, RELEVANCE]) {
          edges {
            role
            node { id name { full native } image { large } siteUrl }
            voiceActors(language: JAPANESE, sort: [RELEVANCE]) {
              id
              name { full native alternative }
              image { large medium }
              siteUrl
            }
          }
        }
        externalLinks { site url type }
     }
              }
            }
        `

func (e *Explorer) SourceAniListRecognition(ctx context.Context, detail, proxy bool, value any) (json.RawMessage, error) {
	query := sourceAniListSearchQuery
	variables := map[string]any{"search": value, "count": 20}
	if detail {
		query = sourceAniListDetailQuery
		variables = map[string]any{"id": value}
	}
	source, path := "anilist", "/"
	if proxy {
		source, path = "anilist_proxy", "/anilist/"
	}
	var raw json.RawMessage
	err := e.requestProvider(ctx, source, http.MethodPost, path, nil, map[string]any{"query": query, "variables": variables}, &raw, true)
	return raw, err
}
func (e *Explorer) SourceAniListTranslationData(ctx context.Context) (json.RawMessage, error) {
	var raw json.RawMessage
	err := e.requestProvider(ctx, "anilist_translations", http.MethodGet, "/soruly/anilist-chinese/master/anilist-chinese.json", nil, nil, &raw, true)
	return raw, err
}
