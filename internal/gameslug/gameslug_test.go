package gameslug

import "testing"

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"":                     "",
		"Apex Legends":         "apex-legends",
		"Counter-Strike 2":     "counter-strike-2",
		"World of Warcraft":    "world-of-warcraft",
		"Dead by Daylight":     "dead-by-daylight",
		"Dota 2":               "dota-2",
		"Tom's Game!!":         "toms-game",
		"  spaced  out  ":      "spaced-out",
		"under_score_name":     "under-score-name",
		"--leading-trailing--": "leading-trailing",
		"!!!":                  "",
		// Regression: punctuation folds to a dash, not dropped — the dropped
		// form resolves to no game on Twitch.
		"Warhammer 40,000: Space Marine II": "warhammer-40-000-space-marine-ii",
		"PUBG: BATTLEGROUNDS":               "pubg-battlegrounds",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestID(t *testing.T) {
	cases := map[string]string{
		"Apex Legends":     "g_apex_legends",
		"Counter-Strike 2": "g_counter_strike_2",
		"":                 "g_",
	}
	for in, want := range cases {
		if got := ID(in); got != want {
			t.Errorf("ID(%q) = %q, want %q", in, got, want)
		}
	}
}
