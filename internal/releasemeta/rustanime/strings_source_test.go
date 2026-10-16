package rustanime

import (
	"reflect"
	"testing"
)

// These tests isolate the source stage contracts, including in-place Found
// transitions; the full parser oracle is exercised separately by its owner.
func stringTestToken(raw, state string, values ...string) *token {
	tok := &token{raw: raw}
	for i, value := range values {
		tok.subtokens = append(tok.subtokens, &subtoken{value: value, found: i < len(state) && state[i] == 'F'})
	}
	return tok
}

func stringTestInside(tok *token, weak, paren, corner bool) *token {
	tok.inside, tok.weak, tok.paren, tok.corner = true, weak, paren, corner
	return tok
}

func stringTestStates(tokens []*token) []string {
	var states []string
	for _, tok := range tokens {
		state := ""
		for _, st := range tok.subtokens {
			if st.found {
				state += "F"
			} else {
				state += "U"
			}
		}
		states = append(states, state)
	}
	return states
}

func TestStringSourceAnimeTitleSelection(t *testing.T) {
	tests := []struct {
		name   string
		tokens []*token
		before elements
		want   []Element
		states []string
	}{
		{"corner prepass retains preceding and bracketed unknowns", []*token{stringTestToken("Before", "U", "Before"), stringTestInside(stringTestToken("題名", "UF", "題名", "1080p"), false, false, true), stringTestToken("After", "U", "After"), stringTestInside(stringTestToken("Group", "U", "Group"), false, false, false)}, nil, []Element{{AnimeTitle, "「題名」"}}, []string{"U", "FF", "F", "U"}},
		{"leading standalone ascii digits consumed", []*token{stringTestToken("2024", "U", "2024"), stringTestToken("Title", "U", "Title")}, nil, []Element{{AnimeTitle, "Title"}}, []string{"F", "F"}},
		{"known episode skips mixed token", []*token{stringTestToken("1080p Remaining", "FU", "1080p", "Remaining"), stringTestToken("Title", "U", "Title")}, elements{{EpisodeNumber, "01"}}, []Element{{EpisodeNumber, "01"}, {AnimeTitle, "Title"}}, []string{"FU", "F"}},
		{"without known episode mixed token remains eligible", []*token{stringTestToken("1080p Remaining", "FU", "1080p", "Remaining")}, nil, []Element{{AnimeTitle, "Remaining"}}, []string{"FF"}},
		{"punctuation outside is not a title", []*token{stringTestToken(" -- ", "U", "--"), stringTestToken("Title", "U", "Title")}, nil, []Element{{AnimeTitle, "Title"}}, []string{"U", "F"}},
		{"larger bracket unknown count wins", []*token{stringTestInside(stringTestToken("Long Title", "UU", "Long", "Title"), false, false, false), stringTestInside(stringTestToken("Group", "U", "Group"), false, false, false)}, nil, []Element{{AnimeTitle, "Long Title"}}, []string{"FF", "U"}},
		{"bracket tie selects second", []*token{stringTestInside(stringTestToken("First", "U", "First"), false, false, false), stringTestInside(stringTestToken("Second", "U", "Second"), false, false, false)}, nil, []Element{{AnimeTitle, "Second"}}, []string{"U", "F"}},
		{"one bracket has no fallback title", []*token{stringTestInside(stringTestToken("Single", "U", "Single"), false, false, false)}, nil, nil, []string{"U"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := test.before
			parseAnimeTitle(test.tokens, &got, " .-_")
			if !reflect.DeepEqual([]Element(got), test.want) || !reflect.DeepEqual(stringTestStates(test.tokens), test.states) {
				t.Fatalf("elements=%#v states=%v; want %#v %v", got, stringTestStates(test.tokens), test.want, test.states)
			}
		})
	}
}

func TestStringSourceFindAnimeTitle(t *testing.T) {
	tests := []struct {
		name   string
		tokens []*token
		before elements
		want   string
		states []string
	}{
		{"known prefix and punctuation immediately before known", []*token{stringTestToken("1080p Title - 01 Later", "FUUFU", "1080p", "Title", "-", "01", "Later")}, nil, "Title", []string{"FFFFU"}},
		{"dot initials preserve original separators", []*token{stringTestToken("S.A.O.01", "UUUF", "S", "A", "O", "01")}, nil, "S.A.O", []string{"FFFF"}},
		{"dot limit counts bytes", []*token{stringTestToken("名.字", "UU", "名", "字")}, nil, "名 字", []string{"FF"}},
		{"hyphen alphabetic before known", []*token{stringTestToken("Hellsing-Ultimate 01", "UUF", "Hellsing", "Ultimate", "01")}, nil, "Hellsing-Ultimate", []string{"FFF"}},
		{"hyphen standalone digits", []*token{stringTestToken("8-6 01", "UUF", "8", "6", "01")}, nil, "8-6", []string{"FFF"}},
		{"hyphen alphabetic requires next known", []*token{stringTestToken("Hellsing-Ultimate Final", "UUU", "Hellsing", "Ultimate", "Final")}, nil, "Hellsing Ultimate Final", []string{"FFF"}},
		{"invalid episode prefix retains alpha on match", []*token{stringTestToken("Title EX01", "UU", "Title", "EX01")}, elements{{EpisodeNumber, "01"}}, "Title EX", []string{"FF"}},
		{"type prefix retains alpha on fractional match", []*token{stringTestToken("Title OVA3.5", "UU", "Title", "OVA3.5")}, elements{{EpisodeNumber, "3.5"}}, "Title OVA", []string{"FF"}},
		{"episode integer prefix of fractional value", []*token{stringTestToken("Title EX3", "UU", "Title", "EX3")}, elements{{EpisodeNumber, "3.5"}}, "Title EX", []string{"FF"}},
		{"fractional suffix does not match integer episode", []*token{stringTestToken("Title EX3.5", "UU", "Title", "EX3.5")}, elements{{EpisodeNumber, "3"}}, "Title EX3.5", []string{"FF"}},
		{"invalid suffix retains whole token", []*token{stringTestToken("Title EX01a", "UU", "Title", "EX01a")}, elements{{EpisodeNumber, "01"}}, "Title EX01a", []string{"FF"}},
		{"valid episode prefix terminates", []*token{stringTestToken("Title EP01 Tail", "UUU", "Title", "EP01", "Tail")}, elements{{EpisodeNumber, "01"}}, "Title", []string{"FFU"}},
		{"invalid anime type also terminates", []*token{stringTestToken("Title OP4a Tail", "UUU", "Title", "OP4a", "Tail")}, nil, "Title", []string{"FFU"}},
		{"other keyword prefix terminates", []*token{stringTestToken("Title AAC2 Tail", "UUU", "Title", "AAC2", "Tail")}, nil, "Title", []string{"FFU"}},
		{"weak bracket adjacency", []*token{stringTestToken("Title", "U", "Title"), stringTestInside(stringTestToken("Part Two", "UU", "Part", "Two"), true, true, false)}, nil, "Title(Part Two)", []string{"F", "FF"}},
		{"weak bracket following delimiter", []*token{stringTestToken("Title ", "U", "Title"), stringTestInside(stringTestToken("Part Two", "UU", "Part", "Two"), true, true, false)}, nil, "Title (Part Two)", []string{"F", "FF"}},
		{"embedded bracket requires following outside unknown", []*token{stringTestToken("Title", "U", "Title"), stringTestInside(stringTestToken("Extra", "U", "Extra"), false, false, false), stringTestToken("More", "U", "More")}, nil, "Title[Extra] More", []string{"F", "F", "F"}},
		{"final strong bracket terminates", []*token{stringTestToken("Title", "U", "Title"), stringTestInside(stringTestToken("Group", "U", "Group"), false, false, false)}, nil, "Title", []string{"F", "U"}},
		{"outside dash suffix", []*token{stringTestToken("Title", "U", "Title"), stringTestToken(" - Subtitle", "U", "Subtitle")}, nil, "Title - Subtitle", []string{"F", "F"}},
		{"found mid bracket returns before closing bracket", []*token{stringTestToken("Title", "U", "Title"), stringTestInside(stringTestToken("Part 1080p Tail", "UFU", "Part", "1080p", "Tail"), true, true, false)}, nil, "Title(Part", []string{"F", "FFU"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := findAnimeTitle(test.tokens, 0, " .-_", &test.before)
			if got != test.want || !reflect.DeepEqual(stringTestStates(test.tokens), test.states) {
				t.Fatalf("title=%q states=%v; want %q %v", got, stringTestStates(test.tokens), test.want, test.states)
			}
		})
	}
}

func TestStringSourceReleaseGroup(t *testing.T) {
	tests := []struct {
		name   string
		tokens []*token
		before elements
		want   []Element
		states []string
	}{
		{"full bracket raw retained", []*token{stringTestInside(stringTestToken(" Group_With.Dots ", "UUU", "Group", "With", "Dots"), false, false, false)}, nil, []Element{{ReleaseGroup, " Group_With.Dots "}}, []string{"FFF"}},
		{"BY suffix joins subtokens", []*token{stringTestInside(stringTestToken("Encoded_by_Team_Name", "UUUU", "Encoded", "by", "Team", "Name"), false, false, false)}, nil, []Element{{ReleaseGroup, "Team Name"}}, []string{"FFFF"}},
		{"BY first is not suffix syntax", []*token{stringTestInside(stringTestToken("BY Team", "UU", "BY", "Team"), false, false, false)}, nil, []Element{{ReleaseGroup, "BY Team"}}, []string{"FF"}},
		{"BY last is not suffix syntax", []*token{stringTestInside(stringTestToken("Encoded BY", "UU", "Encoded", "BY"), false, false, false)}, nil, []Element{{ReleaseGroup, "Encoded BY"}}, []string{"FF"}},
		{"between title spans skipped", []*token{stringTestToken("Title", "U", "Title"), stringTestInside(stringTestToken("Part", "U", "Part"), false, false, false), stringTestToken("More", "U", "More"), stringTestInside(stringTestToken("Group", "U", "Group"), false, false, false)}, nil, []Element{{ReleaseGroup, "Group"}}, []string{"U", "U", "U", "F"}},
		{"partial strong before title", []*token{stringTestInside(stringTestToken("1080p Group x264 Tail", "FUFU", "1080p", "Group", "x264", "Tail"), false, false, false), stringTestToken("Title", "U", "Title")}, nil, []Element{{ReleaseGroup, "Group"}}, []string{"FFFU", "U"}},
		{"partial strong after raw known title excluded", []*token{stringTestToken("Title", "F", "Title"), stringTestInside(stringTestToken("1080p Group", "FU", "1080p", "Group"), false, false, false)}, nil, nil, []string{"F", "FU"}},
		{"partial parenthesis after title allowed", []*token{stringTestToken("Title", "U", "Title"), stringTestInside(stringTestToken("1080p Group", "FU", "1080p", "Group"), false, true, false)}, nil, []Element{{ReleaseGroup, "Group"}}, []string{"U", "FF"}},
		{"codec hyphen fallback", []*token{stringTestToken("x264-ESiR", "FU", "x264", "ESiR")}, nil, []Element{{ReleaseGroup, "ESiR"}}, []string{"FF"}},
		{"dot is not hyphen fallback", []*token{stringTestToken("x264.DHD", "FU", "x264", "DHD")}, nil, nil, []string{"FU"}},
		{"unknown trailing number prevents fallback", []*token{stringTestToken("x264-ESiR 12", "FUU", "x264", "ESiR", "12")}, nil, nil, []string{"FUU"}},
		{"empty trailing unknown permits fallback", []*token{stringTestToken("x264-ESiR", "FUU", "x264", "ESiR", "")}, nil, []Element{{ReleaseGroup, "ESiR"}}, []string{"FFU"}},
		{"multibyte single letter passes byte minimum", []*token{stringTestToken("x264-組", "FU", "x264", "組")}, nil, []Element{{ReleaseGroup, "組"}}, []string{"FF"}},
		{"existing category skips all", []*token{stringTestInside(stringTestToken("Other", "U", "Other"), false, false, false)}, elements{{ReleaseGroup, "Existing"}}, []Element{{ReleaseGroup, "Existing"}}, []string{"U"}},
		{"weak and corner brackets excluded", []*token{stringTestInside(stringTestToken("Weak", "U", "Weak"), true, true, false), stringTestInside(stringTestToken("Corner", "U", "Corner"), false, false, true)}, nil, nil, []string{"U", "U"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := test.before
			parseReleaseGroup(test.tokens, &got, " .-_")
			if !reflect.DeepEqual([]Element(got), test.want) || !reflect.DeepEqual(stringTestStates(test.tokens), test.states) {
				t.Fatalf("elements=%#v states=%v; want %#v %v", got, stringTestStates(test.tokens), test.want, test.states)
			}
		})
	}
}

func TestStringSourceEpisodeAndParticular(t *testing.T) {
	tokens := []*token{
		stringTestInside(stringTestToken("Bracket", "U", "Bracket"), false, false, false),
		stringTestToken("---", "U", "---"),
		stringTestToken("01 - The End 1080p Later", "FUUUFU", "01", "-", "The", "End", "1080p", "Later"),
		stringTestToken("Next", "U", "Next"),
	}
	var got elements
	parseEpisodeTitle(tokens, &got, " .-_")
	if !reflect.DeepEqual(got, elements{{EpisodeTitle, "The End"}}) || !reflect.DeepEqual(stringTestStates(tokens), []string{"U", "U", "FFFFFU", "U"}) {
		t.Fatalf("elements=%#v states=%v", got, stringTestStates(tokens))
	}
	// A consumed empty string still creates an empty element at end-of-token,
	// while a Found boundary suppresses an empty trimmed value in the source.
	for _, boundary := range []bool{false, true} {
		tok := stringTestToken("", "U", "")
		if boundary {
			tok.subtokens = append(tok.subtokens, &subtoken{value: "x264", found: true})
		}
		got = nil
		parseParticularStringSubtoken(tok, &got, ReleaseGroup, " ")
		if boundary && len(got) != 0 || !boundary && !reflect.DeepEqual(got, elements{{ReleaseGroup, ""}}) {
			t.Fatalf("boundary=%v elements=%#v", boundary, got)
		}
	}
	got = nil
	parseParticularStringSubtoken(stringTestToken("No Spaces Trimmed", "UUU", "No", "Spaces", "Trimmed"), &got, EpisodeTitle, "_")
	if !reflect.DeepEqual(got, elements{{EpisodeTitle, " No Spaces Trimmed"}}) {
		t.Fatalf("custom delimiter unexpectedly trimmed inserted spaces: %#v", got)
	}
}

func TestStringSourceMultipleKeyword(t *testing.T) {
	manager := keywordManager{
		"HI.10":        {category: VideoTerm},
		"HI 10":        {category: AudioTerm},
		"ALPHA-BETA":   {category: VideoResolution},
		"FIRST_SECOND": {category: AnimeTitle},
	}
	var got elements
	if !parseMultipleKeyword(&got, manager, " Hi", "10 ") || !reflect.DeepEqual(got, elements{{VideoTerm, "Hi.10"}}) {
		t.Fatalf("separator priority or raw case lost: %#v", got)
	}
	if !parseMultipleKeyword(&got, manager, "Hi", "10") || len(got) != 2 {
		t.Fatalf("nonsingular keyword not repeated: %#v", got)
	}
	got = elements{{VideoResolution, "1080p"}}
	if parseMultipleKeyword(&got, manager, "Alpha", "Beta") || len(got) != 1 {
		t.Fatalf("singular keyword overwritten: %#v", got)
	}
	got = nil
	if parseMultipleKeyword(&got, manager, "First", "Second") || len(got) != 0 {
		t.Fatalf("nonsearchable category accepted: %#v", got)
	}
}

func TestStringSourceUnicodeProperties(t *testing.T) {
	if !stringAlphabetic('\u0345') || !stringAlphabetic('Ⅷ') || !stringAlphanumeric('Ⅷ') || !stringUppercase('Ⓐ') || stringAlphabetic('1') || stringASCIIDigits("１２") {
		t.Fatal("Rust alphabetic/alphanumeric and ASCII-digit distinctions lost")
	}
}
