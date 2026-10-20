package app

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"

	"light-media/internal/releasemeta"
	"light-media/internal/sites"
)

func TestSubscriptionAcquisitionOriginalFailureHelpers(t *testing.T) {
	var oracle struct {
		Fingerprint []struct{ Kind, Resource, Site, Season, Episode, Fingerprint string }
		TTL         []struct {
			Message string
			Seconds int
		}
		Prefix []struct {
			Value  *string
			Result bool
		}
		Resource []struct {
			Input  map[string]json.RawMessage
			Result string
		}
	}
	raw, e := os.ReadFile("testdata/acquisition-failure-source-oracle.json")
	if e != nil || json.Unmarshal(raw, &oracle) != nil {
		t.Fatal(e)
	}
	a, _, _ := importTestApp(t)
	ctx := context.Background()
	m := importFixture(t, a, "movie")
	r := &subscriptionAcquisition{Subscription: Subscription{Media: m}}
	parser, e := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: releasemeta.Options{SourceMode: "legacy"}})
	if e != nil {
		t.Fatal(e)
	}
	for i, c := range oracle.Fingerprint {
		t.Run("fingerprint-"+strconv.Itoa(i), func(t *testing.T) {
			r.Subscription.Media.MediaType = "movie"
			title := "Test 2024"
			if c.Kind == "电视剧" {
				r.Subscription.Media.MediaType = "tv"
				title = "Test S01E01-E02"
			}
			meta, e := parser.ParseTitle(title, nil, nil)
			if e != nil {
				t.Fatal(e)
			}
			a.config.Sites = []SiteConfig{{Config: sites.Config{ID: "no-native-id", Name: c.Site}, Enabled: true}}
			got, e := a.acquisitionFingerprint(ctx, r, Candidate{Resource: sites.Resource{ID: c.Resource, SiteID: "no-native-id"}}, meta)
			if e != nil || got != c.Fingerprint {
				t.Fatal("actual AST failure fingerprint differs", meta.Season(), meta.Episode(), got, c.Fingerprint, e)
			}
		})
	}
	for i, c := range oracle.TTL {
		t.Run("ttl-"+strconv.Itoa(i), func(t *testing.T) {
			if acquisitionFailureTTL(c.Message) != time.Duration(c.Seconds)*time.Second {
				t.Fatal(c)
			}
		})
	}
	for i, c := range oracle.Prefix {
		t.Run("prefix-"+strconv.Itoa(i), func(t *testing.T) {
			value := ""
			if c.Value != nil {
				value = *c.Value
			}
			if isSubscriptionAcquisitionSource(value) != c.Result {
				t.Fatal(c)
			}
		})
	}
	for i, c := range oracle.Resource {
		t.Run("resource-"+strconv.Itoa(i), func(t *testing.T) {
			var resource sites.Resource
			for key, target := range map[string]*string{"torrent_id": &resource.ID, "info_hash": &resource.Hash, "page_url": &resource.DetailsURL, "title": &resource.Title} {
				if b := c.Input[key]; b != nil {
					if json.Unmarshal(b, target) != nil {
						t.Fatal(key)
					}
				}
			}
			if b := c.Input["size"]; b != nil {
				json.Unmarshal(b, &resource.Size)
			}
			if got := acquisitionResourceKey(resource); got != c.Result {
				t.Fatal(got, c.Result)
			}
		})
	}
}
