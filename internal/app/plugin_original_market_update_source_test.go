package app

import (
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

// This host ships its own Go plugin artifacts. A market entry that only mirrors
// the upstream Python plugin must never become an update target, even though its
// version number is higher: the badge has to follow the repository this build
// can actually install from.
func TestUpdateBadgeFollowsInstallableRepositoryNotUpstreamPython(t *testing.T) {
	python := artifacts.Discovery{
		PluginID:          "AutoSignIn",
		CurrentVersion:    "3.0.14",
		RepositoryURL:     "https://github.com/jxxghp/MoviePilot-Plugins",
		UnavailableReason: "原 MoviePilot Python 插件元数据，仅供查看；需重写并发布 Go 制品后才能安装",
	}
	own := artifacts.Discovery{
		PluginID:       "AutoSignIn",
		CurrentVersion: "2.9.15",
		RepositoryURL:  "https://github.com/landuo/light-media-plugins",
	}
	for _, order := range [][]artifacts.Discovery{{python, own}, {own, python}} {
		selected := selectOriginalMarketDeclarations(order)
		if len(selected) != 1 || selected[0].RepositoryURL != own.RepositoryURL {
			t.Fatalf("selection order %v picked %+v, want the installable artifact", order, selected)
		}
		locals := []pluginCard{{InstanceID: "AutoSignIn", Manifest: plugins.Manifest{Version: "2.9.14"}, Installed: true}}
		cards := a_generateCards(locals, selected)
		if len(cards) != 1 || !cards[0].HasUpdate || cards[0].RepositoryVersion != "2.9.15" {
			t.Fatalf("card %+v must offer the repository patch update", cards)
		}
	}
}

func TestUpdateBadgeIgnoresUnavailableDeclarationAlone(t *testing.T) {
	python := artifacts.Discovery{
		PluginID:          "IYUUAutoSeed",
		CurrentVersion:    "2.22",
		RepositoryURL:     "https://github.com/jxxghp/MoviePilot-Plugins",
		UnavailableReason: "原 MoviePilot Python 插件元数据，仅供查看；需重写并发布 Go 制品后才能安装",
	}
	locals := []pluginCard{{InstanceID: "IYUUAutoSeed", Manifest: plugins.Manifest{Version: "2.19"}, Installed: true}}
	cards := a_generateCards(locals, []artifacts.Discovery{python})
	if len(cards) != 1 {
		t.Fatalf("expected one card, got %+v", cards)
	}
	if cards[0].HasUpdate {
		t.Fatalf("upstream-only metadata must not raise an update badge: %+v", cards[0])
	}
	if cards[0].UpdateBlockedReason == "" {
		t.Fatal("view-only declarations still have to explain why they cannot be installed")
	}
}

func TestUpdateBadgeComparesVersionsInsideInstallableRepository(t *testing.T) {
	locals := []pluginCard{{InstanceID: "RemoveLink", Manifest: plugins.Manifest{Version: "2.17"}, Installed: true}}
	cases := []struct {
		version string
		want    bool
	}{
		{"2.18", true},
		{"2.17", false},
		{"2.16", false},
	}
	for _, c := range cases {
		source := artifacts.Discovery{PluginID: "RemoveLink", CurrentVersion: c.version, RepositoryURL: "https://github.com/landuo/light-media-plugins"}
		cards := a_generateCards(locals, []artifacts.Discovery{source})
		if len(cards) != 1 || cards[0].HasUpdate != c.want {
			t.Fatalf("repository version %s: got %+v, want has_update=%v", c.version, cards, c.want)
		}
	}
}

// a_generateCards keeps the tests independent of the App receiver: the market
// card assembly only needs the installed cards and the declarations.
func a_generateCards(locals []pluginCard, sources []artifacts.Discovery) []pluginCard {
	a := &App{}
	return a.originalMarketCards(locals, sources, map[string]bool{})
}
