package media

import "testing"

func TestMovieReleaseNamesKeepTechnicalSuffixOutOfIdentity(t *testing.T) {
	for _, name := range []string{
		"Joker.Folie.a.Deux.2024.2160p.AMZN.WEB-DL.H265.HDR.DDP5.1-ADWeb.mkv",
		"Joker.Folie.a.Deux.2024.1080p.NF.WEB-DL.H264.DDP5.1-TEST.mkv",
		"Joker.Folie.a.Deux.2024.2160p.BluRay.DTS-HD.MA5.1.x265-TEST.mkv",
	} {
		p := Parse(name)
		if p.Status != Parsed || p.Title != "Joker Folie a Deux" || p.Year != 2024 || p.Type != "movie" {
			t.Fatalf("%s: %+v", name, p)
		}
	}
	if p := Parse("Joker.2024.This.Is.Another.Film-TEST.mkv"); p.Status == Parsed {
		t.Fatal("arbitrary second title accepted")
	}
	if NormalizeTitle("Joker: Folie à Deux") != NormalizeTitle("Joker.Folie.a.Deux") {
		t.Fatal("release accent variant rejected")
	}
}
