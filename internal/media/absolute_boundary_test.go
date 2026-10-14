package media

import "testing"

func TestAdjacentAbsoluteMarkersNeverDropEpisodes(t *testing.T) {
	for _, name := range []string{"Show EP1 EP2.mkv", "Show EP1 EP2 EP3.mkv", "Show 第1集 第2集.mkv", "Show EP1_EP2.mkv", "Show EP1234X.mkv", "Show EP12345.mkv"} {
		if got := Parse(name); got.Status != NeedsReview {
			t.Errorf("ambiguous numbering accepted for %s: %+v", name, got)
		}
	}
	for _, name := range []string{"Show EP1.mkv", "Show 第1集.mkv", "Show EP1 1080p.mkv"} {
		if got := Parse(name); got.Status != Parsed || got.Original.Episodes[0] != 1 {
			t.Errorf("single episode failed for %s: %+v", name, got)
		}
	}
}
