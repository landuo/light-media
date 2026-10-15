package catalog

import (
	"context"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Person returns actor/crew biography and aliases. Missing Chinese biographies
// are filled from the same person's English record when available.
func (c *Client) Person(ctx context.Context, id int64) (Person, error) {
	if !validID(id) {
		return Person{}, &Error{Code: "invalid_input"}
	}
	path := "person/" + strconv.FormatInt(id, 10)
	var response Person
	if err := c.get(ctx, path, nil, &response); err != nil {
		return Person{}, err
	}
	if response.ID != id {
		return Person{}, &Error{Code: "invalid_response"}
	}
	if strings.TrimSpace(response.Biography) == "" || strings.TrimSpace(response.Name) == "" {
		var fallback Person
		if c.get(ctx, path, url.Values{"language": {"en-US"}}, &fallback) == nil && fallback.ID == id {
			if strings.TrimSpace(response.Biography) == "" {
				response.Biography = fallback.Biography
			}
			if strings.TrimSpace(response.Name) == "" {
				response.Name = fallback.Name
			}
		}
	}
	response.Name = c.text(response.Name, 512)
	if response.Name == "" || len(response.AlsoKnownAs) > 200 {
		return Person{}, &Error{Code: "invalid_response"}
	}
	if _, _, valid := date(response.Birthday); !valid {
		return Person{}, &Error{Code: "invalid_response"}
	}
	if _, _, valid := date(response.Deathday); !valid {
		return Person{}, &Error{Code: "invalid_response"}
	}
	response.Biography, response.PlaceOfBirth = c.text(response.Biography, 64000), c.text(response.PlaceOfBirth, 1024)
	response.KnownForDepartment, response.ProfilePath = c.text(response.KnownForDepartment, 256), c.image(response.ProfilePath)
	aliases := make([]string, 0, len(response.AlsoKnownAs))
	for _, alias := range response.AlsoKnownAs {
		if value := c.text(alias, 512); value != "" {
			aliases = append(aliases, value)
		}
	}
	response.AlsoKnownAs = aliases
	return response, nil
}

// PersonCredits returns the non-paginated combined movie/TV cast and crew
// filmography as one page. A title appears once; its roles/jobs are preserved.
func (c *Client) PersonCredits(ctx context.Context, id int64) (Page, error) {
	if !validID(id) {
		return Page{}, &Error{Code: "invalid_input"}
	}
	var response struct {
		ID   int64       `json:"id"`
		Cast []wireMedia `json:"cast"`
		Crew []wireMedia `json:"crew"`
	}
	if err := c.get(ctx, "person/"+strconv.FormatInt(id, 10)+"/combined_credits", nil, &response); err != nil {
		return Page{}, err
	}
	if response.ID != id || response.Cast == nil || response.Crew == nil || len(response.Cast)+len(response.Crew) > 10000 {
		return Page{}, &Error{Code: "invalid_response"}
	}
	result := Page{Results: []Media{}, Page: 1, TotalPages: 1}
	indices := map[string]int{}
	entries := append(response.Cast, response.Crew...)
	for _, entry := range entries {
		if entry.Adult {
			continue
		}
		media, err := c.media(entry, "all", false)
		if err != nil {
			return Page{}, err
		}
		key := media.MediaType + "/" + strconv.FormatInt(media.ID, 10)
		if index, exists := indices[key]; exists {
			prior := &result.Results[index]
			prior.Character = joinRole(prior.Character, media.Character)
			prior.Job = joinRole(prior.Job, media.Job)
			continue
		}
		indices[key] = len(result.Results)
		result.Results = append(result.Results, media)
	}
	sort.SliceStable(result.Results, func(i, j int) bool {
		left, right := result.Results[i], result.Results[j]
		if left.Popularity != right.Popularity {
			return left.Popularity > right.Popularity
		}
		if left.Year != right.Year {
			return left.Year > right.Year
		}
		if left.MediaType != right.MediaType {
			return left.MediaType < right.MediaType
		}
		return left.ID < right.ID
	})
	result.TotalResults = len(result.Results)
	return result, nil
}

func joinRole(prior, next string) string {
	if next == "" || prior == next {
		return prior
	}
	if prior == "" {
		return next
	}
	for _, value := range strings.Split(prior, " / ") {
		if value == next {
			return prior
		}
	}
	joined := prior + " / " + next
	if len([]rune(joined)) > 2048 {
		return prior
	}
	return joined
}
