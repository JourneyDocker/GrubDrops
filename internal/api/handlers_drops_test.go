package api

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JourneyDocker/grubdrops/internal/store"
	"github.com/JourneyDocker/grubdrops/internal/store/gen"
	"github.com/JourneyDocker/grubdrops/internal/web"
)

func renderDropsTable(t *testing.T, page dropsPage) string {
	t.Helper()
	tmpl, err := web.Templates()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "drops_table", page); err != nil {
		t.Fatalf("render drops_table: %v", err)
	}
	return buf.String()
}

// TestDropsTable_ColdStartCTA verifies the cold-start trap fix: when no
// games are whitelisted at all (NoWhitelist), discovery never runs and the
// page would otherwise be silently empty. We must show a bootstrap CTA that
// links to where the user can add a game, instead of the misleading
// "discovery populates this list" empty text.
func TestDropsTable_ColdStartCTA(t *testing.T) {
	out := renderDropsTable(t, dropsPage{Tab: tabCurrent, NoWhitelist: true})
	if !strings.Contains(strings.ToLower(out), "no games whitelisted") {
		t.Errorf("cold-start panel missing the 'no games whitelisted' explanation")
	}
	if !strings.Contains(out, `href="/priority"`) {
		t.Errorf("cold-start panel must link to the Priority page where games are added")
	}
}

// TestDropsTable_NoColdStartWhenWhitelisted verifies the CTA does NOT appear
// once any game is whitelisted (NoWhitelist false) — the normal case.
func TestDropsTable_NoColdStartWhenWhitelisted(t *testing.T) {
	out := renderDropsTable(t, dropsPage{Tab: tabCurrent, NoWhitelist: false})
	if strings.Contains(strings.ToLower(out), "no games whitelisted") {
		t.Errorf("cold-start panel should not render when a whitelist exists")
	}
}

func renderCampaignItems(t *testing.T, detail campaignDetailRow) string {
	t.Helper()
	tmpl, err := web.Templates()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "drops_campaign_items", detail); err != nil {
		t.Fatalf("render drops_campaign_items: %v", err)
	}
	return buf.String()
}

// TestCampaignItems_CollectedMarkIsClickableUncollect verifies the manual
// un-collect escape hatch: each COLLECTED mark renders as an hx-post button
// carrying the exact (account_id, benefit_id, campaign_id) to delete, plus a
// confirm prompt. No visible "X" — the action is the chip itself.
func TestCampaignItems_CollectedMarkIsClickableUncollect(t *testing.T) {
	detail := campaignDetailRow{
		ID:        "camp-1",
		CSRFToken: "tok123",
		Benefits: []campaignBenefitRow{{
			Name:            "Esports Pack",
			RequiredMinutes: 60,
			Collected: []collectedMark{{
				Login: "TTik3r", Platform: "kick",
				AccountID: "acc-9", BenefitID: "ben-5",
			}},
		}},
	}
	out := renderCampaignItems(t, detail)

	for _, want := range []string{
		`hx-post="/drops/claim/remove"`,
		`hx-confirm=`,
		`"account_id":"acc-9"`,
		`"benefit_id":"ben-5"`,
		`"campaign_id":"camp-1"`,
		`tok123`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("un-collect control missing %q\n--- rendered ---\n%s", want, out)
		}
	}
	if strings.Contains(out, "✕") || strings.Contains(out, " x<") {
		t.Errorf("un-collect mark must not show a visible X")
	}
}

// newDropsLinkDeps spins up a migrated sqlite DB with two enabled twitch
// accounts, both whitelisting "Rocket League" and both with a CHECKED-but-
// unlinked link state on campaign "camp-1" — i.e. exactly the shape the
// "Whitelisted — account not linked" pane exists for.
func newDropsLinkDeps(t *testing.T) (*dropsDeps, *store.Settings) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "drops-link.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	q := gen.New(db)
	s := store.NewSettings(q)

	now := time.Now().Unix()
	for _, g := range []struct{ id, name, slug string }{
		{"rl", "Rocket League", "rocket-league"},
		{"ttga", "Test Game Alpha", "test-game-alpha"},
	} {
		if err := q.UpsertGame(ctx, gen.UpsertGameParams{ID: g.id, Name: g.name, Slug: g.slug, Priority: 1}); err != nil {
			t.Fatalf("upsert game %s: %v", g.id, err)
		}
	}
	if err := q.UpsertCampaign(ctx, gen.UpsertCampaignParams{
		ID: "camp-1", Platform: "twitch", Game: "Rocket League", Name: "Rocket League Crate",
		StartsAt: now, EndsAt: now + 86400, Status: "active", RawJson: "{}",
		DiscoveredAt: now, Kind: "drop", AccountLinked: 0, AccountLinkUrl: "https://example.test/connect",
	}); err != nil {
		t.Fatalf("upsert campaign: %v", err)
	}
	for _, id := range []string{"acc-a", "acc-b"} {
		if _, err := q.CreateAccount(ctx, gen.CreateAccountParams{
			ID: id, Platform: "twitch", DisplayName: "player-" + id,
			Status: "idle", FingerprintJson: "{}", Enabled: 1,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("create account %s: %v", id, err)
		}
		if err := q.AddAccountGame(ctx, gen.AddAccountGameParams{AccountID: id, GameID: "rl", Rank: 1}); err != nil {
			t.Fatalf("whitelist game for %s: %v", id, err)
		}
		if err := q.UpsertAccountCampaignLink(ctx, gen.UpsertAccountCampaignLinkParams{
			AccountID: id, CampaignID: "camp-1",
			Linked: 0, Checked: 1, LinkUrl: "https://example.test/link/" + id,
			UpdatedAt: now,
		}); err != nil {
			t.Fatalf("record unlinked link state for %s: %v", id, err)
		}
	}
	return &dropsDeps{q: q, s: s}, s
}

func linkTestRow() dropsRow {
	return dropsRow{
		CampaignID:   "camp-1",
		CampaignName: "Rocket League Crate",
		Game:         "Rocket League",
		Platform:     "twitch",
		Kind:         "drop",
		LinkURL:      "https://example.test/connect",
		When:         "12:00",
	}
}

// TestDropsLinkGrouping_MineUnlinkedFlags covers the "Whitelisted — account not
// linked" pane's claim that these campaigns are NOT mined. That claim is only
// true while mine-unlinked is off: with either the global override or the
// relevant account's per-account flag on, the watcher mines them anyway
// (watcher.go: ForceLinked → MineUnlinked → skip), so they belong in the main
// "Whitelisted" table and the pane must be gone.
func TestDropsLinkGrouping_MineUnlinkedFlags(t *testing.T) {
	ctx := context.Background()
	unlinkedHeading := "Whitelisted — account not linked"

	t.Run("both flags off keeps the campaign in the not-linked pane", func(t *testing.T) {
		d, _ := newDropsLinkDeps(t)
		wl, plat := d.accountWhitelists(ctx)
		row := linkTestRow()
		mu := d.resolveMineUnlinked(ctx, wl)
		mineable, chips := d.linkGrouping(ctx, &row, wl, plat, mu)
		row.ConnectChips = chips // what list() does before the split
		if mineable {
			t.Fatal("with mine-unlinked off the unlinked campaign must not be mineable")
		}
		if len(chips) != 2 || !row.NeedsConnect {
			t.Fatalf("expected 2 unlinked connect chips and NeedsConnect, got %d chips / NeedsConnect=%v", len(chips), row.NeedsConnect)
		}
		out := renderDropsTable(t, dropsPage{Tab: tabCurrent, UnlinkedRows: []dropsRow{row}})
		if !strings.Contains(out, unlinkedHeading) {
			t.Errorf("not-linked pane must render when mine-unlinked is off")
		}
	})

	t.Run("global flag on promotes to the main table", func(t *testing.T) {
		d, s := newDropsLinkDeps(t)
		if err := s.SetMineUnlinkedGlobal(ctx, true); err != nil {
			t.Fatalf("set global mine-unlinked: %v", err)
		}
		wl, plat := d.accountWhitelists(ctx)
		row := linkTestRow()
		mu := d.resolveMineUnlinked(ctx, wl)
		mineable, chips := d.linkGrouping(ctx, &row, wl, plat, mu)
		row.ConnectChips = chips // what list() does before the split
		if !mineable {
			t.Fatal("global mine-unlinked must make the unlinked campaign mineable")
		}
		// Chips stay honest: the accounts really are unlinked.
		if len(chips) != 2 {
			t.Fatalf("expected the 2 unlinked chips to survive, got %d", len(chips))
		}
		for _, c := range chips {
			if c.Linked {
				t.Errorf("mine-unlinked must never mark account %s connected", c.Login)
			}
		}
		if !row.NeedsConnect {
			t.Error("NeedsConnect must stay true — the account is still unlinked")
		}
		// The split puts it in Rows, so the not-linked pane disappears.
		out := renderDropsTable(t, dropsPage{Tab: tabCurrent, Rows: []dropsRow{row}})
		if strings.Contains(out, unlinkedHeading) {
			t.Errorf("not-linked pane must be hidden once every row is mineable:\n%s", out)
		}
		if !strings.Contains(out, "player-acc-a →") {
			t.Errorf("main pane must still show the unlinked (connect) chip")
		}
		if strings.Contains(out, "✓ player-acc-a") {
			t.Errorf("main pane must not render a false connected tick for an unlinked account")
		}
	})

	t.Run("per-account flag on for the whitelisting account promotes", func(t *testing.T) {
		d, _ := newDropsLinkDeps(t)
		if err := d.q.UpsertSettingString(ctx, gen.UpsertSettingStringParams{Key: MineUnlinkedKey("acc-a"), Value: []byte("1")}); err != nil {
			t.Fatalf("set per-account mine-unlinked: %v", err)
		}
		wl, plat := d.accountWhitelists(ctx)
		row := linkTestRow()
		mu := d.resolveMineUnlinked(ctx, wl)
		if !mu.perAcc["acc-a"] || mu.perAcc["acc-b"] {
			t.Fatalf("per-account flags resolved wrong: %+v", mu.perAcc)
		}
		if mineable, _ := d.linkGrouping(ctx, &row, wl, plat, mu); !mineable {
			t.Fatal("the whitelisting account's own flag must make the campaign mineable")
		}
		out := renderDropsTable(t, dropsPage{Tab: tabCurrent, Rows: []dropsRow{row}})
		if strings.Contains(out, unlinkedHeading) {
			t.Errorf("not-linked pane must be hidden when a per-account flag promotes the row")
		}
	})

	t.Run("per-account flag on for a NON-whitelisting account does not promote", func(t *testing.T) {
		d, _ := newDropsLinkDeps(t)
		// acc-c whitelists a different game, so its flag must not widen camp-1.
		now := time.Now().Unix()
		if _, err := d.q.CreateAccount(ctx, gen.CreateAccountParams{
			ID: "acc-c", Platform: "twitch", DisplayName: "player-acc-c",
			Status: "idle", FingerprintJson: "{}", Enabled: 1, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("create acc-c: %v", err)
		}
		if err := d.q.AddAccountGame(ctx, gen.AddAccountGameParams{AccountID: "acc-c", GameID: "ttga", Rank: 1}); err != nil {
			t.Fatalf("whitelist game for acc-c: %v", err)
		}
		if err := d.q.UpsertSettingString(ctx, gen.UpsertSettingStringParams{Key: MineUnlinkedKey("acc-c"), Value: []byte("1")}); err != nil {
			t.Fatalf("set acc-c flag: %v", err)
		}
		wl, plat := d.accountWhitelists(ctx)
		row := linkTestRow()
		mu := d.resolveMineUnlinked(ctx, wl)
		if mineable, _ := d.linkGrouping(ctx, &row, wl, plat, mu); mineable {
			t.Fatal("a flag on an account that doesn't whitelist this game must not promote it")
		}
	})
}
