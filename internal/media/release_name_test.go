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

func TestMovieITunesReleaseNames(t *testing.T) {
	for _, platform := range []string{"iT", "IT", "it", "iTunes"} {
		name := "Moana.2026.2160p." + platform + ".WEB-DL.DDP5.1.Atmos.H.265-DepWeb.mkv"
		p := Parse(name)
		if p.Status != Parsed || p.Title != "Moana" || p.Year != 2026 || p.Type != "movie" {
			t.Fatalf("%s: %+v", name, p)
		}
	}
	for _, name := range []string{
		"Moana.2026.2160p.It.Is.Another.Film.WEB-DL.DDP5.1.Atmos.H.265-DepWeb.mkv",
		"Moana.2026.2160p.iT.UNKNOWN.WEB-DL.DDP5.1.Atmos.H.265-DepWeb.mkv",
	} {
		if p := Parse(name); p.Status == Parsed {
			t.Fatalf("unrecognized title or suffix accepted: %s: %+v", name, p)
		}
	}
	if p := Parse("It.2017.2160p.iT.WEB-DL.H.265-DepWeb.mkv"); p.Status != Parsed || p.Title != "It" || p.Year != 2017 {
		t.Fatalf("platform marker changed film title: %+v", p)
	}
}
