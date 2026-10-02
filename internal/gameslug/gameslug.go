// Package gameslug normalizes game names into the canonical slug + id forms
// used across the app (Twitch directory lookups, the games table, campaign
// persistence). Previously each package carried its own copy (twitch
// slugify, api slugifyGame, store slugFromName/gameIDFromName); they could
// drift on edge cases. This is the single source of truth.
package gameslug

import "strings"

// Slug normalizes a game name to its canonical dash slug:
//
//	"Apex Legends"      -> "apex-legends"
//	"Counter-Strike 2"  -> "counter-strike-2"
//	"Tom's Game!!"      -> "toms-game"
//
// Lowercases, drops apostrophes entirely, then collapses every run of
// non [a-z0-9] characters (spaces, dashes, underscores, commas, colons,
// periods, ...) to a single dash, trimming leading/trailing dashes.
// Punctuation such as commas and colons becomes a dash ("Warhammer 40,000:
// Space Marine II" -> "warhammer-40-000-space-marine-ii"); the previous
// implementation dropped them ("warhammer-40000-..."), a slug Twitch does
// not resolve, so the directory returned no game and no channels.
//
// NOTE: this intentionally deviates from DevilXD/TwitchDropsMiner's
// Game.slug derivation (Python `\W+` -> `-`), which preserves underscores
// (`under_score_name` stays as-is upstream). Here underscores fold to
// dashes (`under_score_name` -> `under-score-name`) to match this repo's
// long-standing canonical-slug / game-id convention.
func Slug(name string) string {
	lower := strings.ToLower(name)
	// Apostrophes are removed, not dashed ("Tom's" -> "toms").
	lower = strings.ReplaceAll(lower, "'", "")
	var b strings.Builder
	b.Grow(len(lower))
	prevDash := true // suppress leading dashes
	for _, r := range lower {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// ID returns the internal game id: "g_" + the slug with dashes as
// underscores ("Apex Legends" -> "g_apex_legends"). The g_ prefix keeps
// generated ids from colliding with user-entered ones.
func ID(name string) string {
	return "g_" + strings.ReplaceAll(Slug(name), "-", "_")
}
