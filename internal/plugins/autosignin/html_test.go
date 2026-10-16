package autosignin

import (
	"testing"
)

func TestActualInstalledHTMLEvidence(t *testing.T) {
	raw, err := readAutoSignInFixture("html-reference.json")
	if err != nil {
		t.Fatal(err)
	}
	// Underscore fields are mapped explicitly rather than relying on Go's field matching.
	var rows struct {
		Cases []struct {
			Name, HTML string
			Visible    string `json:"visible_html"`
			LoginForm  bool   `json:"has_login_form"`
			Login      bool   `json:"has_login_evidence"`
			Signin     bool   `json:"has_signin_evidence"`
			Challenge  any    `json:"under_challenge"`
		}
		PatternCases []struct {
			Name, HTML string
			Regexes    []string
			Expected   bool
		} `json:"pattern_cases"`
	}
	if err = Decode(raw, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows.Cases) != 94 || len(rows.PatternCases) != 1368 {
		t.Fatal("reference count changed")
	}
	for _, c := range rows.Cases {
		t.Run(c.Name, func(t *testing.T) {
			visible, err := VisibleHTML(c.HTML)
			if err != nil {
				t.Fatal(err)
			}
			if visible != c.Visible {
				t.Errorf("visible\ngot  %s\nwant %s", visible, c.Visible)
			}
			for _, f := range []struct {
				name string
				fn   func(string) (bool, error)
				want bool
			}{{"form", HasLoginForm, c.LoginForm}, {"login", HasLoginEvidence, c.Login}, {"signin", HasSigninEvidence, c.Signin}, {"challenge", UnderChallenge, c.Challenge == true}} {
				got, err := f.fn(c.HTML)
				if err != nil {
					if f.name == "challenge" {
						if obj, ok := c.Challenge.(Object); ok && obj["error"] == err.Error() {
							continue
						}
					}
					t.Fatal(err)
				}
				if got != f.want {
					t.Errorf("%s got %v want %v", f.name, got, f.want)
				}
			}
		})
	}
	for _, c := range rows.PatternCases {
		t.Run(c.Name, func(t *testing.T) {
			got, err := SignInResult(c.HTML, c.Regexes)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.Expected {
				t.Errorf("got %v want %v", got, c.Expected)
			}
		})
	}
}
