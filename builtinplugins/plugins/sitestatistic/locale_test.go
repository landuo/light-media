package sitestatistic

import (
	"encoding/json"
	"os"
	"testing"

	plugins "light-media/builtinplugins/internal/sdk"
)

func TestCapturedResponseLocaleHelper(t *testing.T) {
	raw, e := os.ReadFile("reference/locale-source-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var oracle struct {
		Requests []struct {
			Name, Locale string
			Query        map[string]string
			Headers      map[string]string
		}
		Translations []struct{ Locale, Text, Translated string }
	}
	if e = json.Unmarshal(raw, &oracle); e != nil {
		t.Fatal(e)
	}
	for _, c := range oracle.Requests {
		t.Run(c.Name, func(t *testing.T) {
			request := plugins.HTTPRequest{Headers: c.Headers, Query: map[string][]string{}}
			for k, v := range c.Query {
				request.Query[k] = []string{v}
			}
			if got := requestLocale(request); got != c.Locale {
				t.Fatal("source locale choice changed", got, c.Locale)
			}
		})
	}
	for _, c := range oracle.Translations {
		t.Run(c.Locale+":"+c.Text, func(t *testing.T) {
			if got := translateMessage(c.Text, c.Locale); got != c.Translated {
				t.Fatal("source translation changed", got, c.Translated)
			}
		})
	}
	p, s := fixedPlugin(), newFixture()
	s.indexer = nil
	request := plugins.HTTPRequest{Key: "refresh_by_domain", Method: "GET", Query: map[string][]string{"domain": {"fixture.invalid"}, "locale": {"en"}}, CredentialMatches: map[string]bool{"apikey": true}, Headers: map[string]string{"x-locale": "zh-TW"}}
	var out plugins.HTTPResponse
	_ = json.Unmarshal(invoke(t, p, s, "http", request), &out)
	var response Response
	_ = json.Unmarshal(out.Body, &response)
	if response.Message != "站点 fixture.invalid 不存在" || response.MessageI18n != "Site fixture.invalid does not exist" {
		t.Fatal("locale changed source message or ignored query", response)
	}
	request.Query["locale"] = []string{""}
	_ = json.Unmarshal(invoke(t, p, s, "http", request), &out)
	_ = json.Unmarshal(out.Body, &response)
	if response.MessageI18n != "站點 fixture.invalid 不存在" {
		t.Fatal("locale header not applied", response)
	}
}
