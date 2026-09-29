package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/JourneyDocker/grubdrops/internal/store"
	"github.com/JourneyDocker/grubdrops/internal/store/gen"
	"github.com/JourneyDocker/grubdrops/internal/timeutil"
	"github.com/JourneyDocker/grubdrops/internal/web"
)

func TestSettingsTemplateRenders(t *testing.T) {
	tmpl, err := web.Templates()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}
	var buf bytes.Buffer
	err = tmpl.ExecuteTemplate(&buf, "settings.html", templateData{
		AuthedAdmin: true, CSRFToken: "tok", Active: "notifications",
		Page: settingsPageData{
			GlobalDiscordWebhook: "https://discord.com/api/webhooks/x",
			NotifyAvatarURL:      "https://img/a.png",
			NotifyClaim:          true,
		},
	})
	if err != nil {
		t.Fatalf("render settings.html: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`name="notify_avatar_url"`,        // avatar input wired
		`https://img/a.png`,               // its value rendered
		`hx-post="/settings/notify-test"`, // test button wired
		`id="notify-test-result"`,         // result target present
	} {
		if !strings.Contains(out, want) {
			t.Errorf("settings.html missing %q", want)
		}
	}
}

type fakeNotifier struct {
	calls int
	last  map[string]any
	err   error
}

func (f *fakeNotifier) Notify(_ context.Context, _ string, fields map[string]any) error {
	f.calls++
	f.last = fields
	return f.err
}

// newTestSettings spins up a migrated sqlite-backed settings store + queries.
func newTestSettings(t *testing.T) (*store.Settings, *gen.Queries) {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	q := gen.New(db)
	return store.NewSettings(q), q
}

func TestNotifyTest_FiresSampleAndReportsOK(t *testing.T) {
	s, q := newTestSettings(t)
	if err := s.SetGlobalDiscordWebhook(context.Background(), "https://discord/x"); err != nil {
		t.Fatal(err)
	}
	fn := &fakeNotifier{}
	d := &settingsDeps{notifier: fn, s: s, q: q}

	rec := httptest.NewRecorder()
	d.notifyTest(rec, httptest.NewRequest("POST", "/settings/notify-test", nil))

	if fn.calls != 1 {
		t.Fatalf("expected notifier called once, got %d", fn.calls)
	}
	if got := strings.ToLower(rec.Body.String()); !strings.Contains(got, "sent") {
		t.Fatalf("expected success fragment, got %q", rec.Body.String())
	}
	// Sample must carry the rich fields so the operator sees a real-looking embed.
	for _, k := range []string{"game", "drop", "channel", "platform", "req_min"} {
		if _, ok := fn.last[k]; !ok {
			t.Errorf("sample event missing %q field", k)
		}
	}
}

func TestNotifyTest_ReportsErrorFromNotifier(t *testing.T) {
	s, q := newTestSettings(t)
	_ = s.SetGlobalDiscordWebhook(context.Background(), "https://discord/x")
	fn := &fakeNotifier{err: errors.New("webhook 404")}
	d := &settingsDeps{notifier: fn, s: s, q: q}

	rec := httptest.NewRecorder()
	d.notifyTest(rec, httptest.NewRequest("POST", "/settings/notify-test", nil))

	if got := rec.Body.String(); !strings.Contains(got, "webhook 404") {
		t.Fatalf("expected error surfaced, got %q", got)
	}
}

func TestNotifyTest_NoWebhookConfigured(t *testing.T) {
	// Notifier wired, but no global webhook and no account webhooks → must
	// report honestly and NOT call the notifier (avoids silent Noop success).
	s, q := newTestSettings(t)
	fn := &fakeNotifier{}
	d := &settingsDeps{notifier: fn, s: s, q: q}

	rec := httptest.NewRecorder()
	d.notifyTest(rec, httptest.NewRequest("POST", "/settings/notify-test", nil))

	if fn.calls != 0 {
		t.Fatalf("notifier should not fire with no webhook, got %d calls", fn.calls)
	}
	if got := strings.ToLower(rec.Body.String()); !strings.Contains(got, "no webhook") {
		t.Fatalf("expected 'no webhook' message, got %q", rec.Body.String())
	}
}

func TestNotifyTest_NoNotifierConfigured(t *testing.T) {
	d := &settingsDeps{notifier: nil}
	rec := httptest.NewRecorder()
	d.notifyTest(rec, httptest.NewRequest("POST", "/settings/notify-test", nil))
	if got := strings.ToLower(rec.Body.String()); !strings.Contains(got, "no notifier") {
		t.Fatalf("expected 'no notifier' message, got %q", rec.Body.String())
	}
}

func TestSettings_SSOCard_Enabled(t *testing.T) {
	tmpl, err := web.Templates()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}
	var buf bytes.Buffer
	err = tmpl.ExecuteTemplate(&buf, "settings.html", templateData{
		Active: "security",
		Page: settingsPageData{
			OIDC: settingsOIDC{
				Enabled:      true,
				ProviderName: "authentik",
				Issuer:       "https://auth.ryuzec.dev/application/o/grubdrops/",
				CallbackURL:  "https://drops.ryuzec.dev/auth/oidc/callback",
			},
		},
	})
	if err != nil {
		t.Fatalf("render settings: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"Single sign-on", "authentik", "auth.ryuzec.dev", "drops.ryuzec.dev/auth/oidc/callback"} {
		if !strings.Contains(out, want) {
			t.Errorf("settings missing %q", want)
		}
	}
}

func TestSettings_SSOCard_Disabled(t *testing.T) {
	tmpl, err := web.Templates()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}
	var buf bytes.Buffer
	err = tmpl.ExecuteTemplate(&buf, "settings.html", templateData{
		Active: "security",
		Page:   settingsPageData{OIDC: settingsOIDC{Enabled: false}},
	})
	if err != nil {
		t.Fatalf("render settings: %v", err)
	}
	if !strings.Contains(buf.String(), "Not configured") {
		t.Errorf("expected disabled SSO card to show 'Not configured'")
	}
}

func renderSettingsTab(t *testing.T, active string, page settingsPageData) string {
	t.Helper()
	tmpl, err := web.Templates()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "settings.html", templateData{Active: active, Page: page}); err != nil {
		t.Fatalf("render: %v", err)
	}
	return buf.String()
}

func TestSettingsTabs_SubnavHasAllLinks(t *testing.T) {
	out := renderSettingsTab(t, "settings", settingsPageData{})
	for _, want := range []string{
		`href="/settings"`,
		`href="/settings/notifications"`, `href="/settings/security"`,
		`href="/settings/accounts"`, `href="/settings/experimental"`,
		`href="/settings/health"`,
		"General", "Notifications", "Security", "Accounts", "Experimental", "Health",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("subnav missing %q", want)
		}
	}
	// Drop Priority moved out of Settings to the top-level /priority nav item.
	if strings.Contains(out, `href="/settings/priority"`) {
		t.Errorf("settings subnav should no longer link to priority (moved to /priority)")
	}
}

func TestSettingsTabs_GeneralSectionOnly(t *testing.T) {
	out := renderSettingsTab(t, "settings", settingsPageData{})
	if !strings.Contains(out, `name="tick_interval_sec"`) {
		t.Errorf("general tab should show interval fields")
	}
	if strings.Contains(out, `action="/settings/global-games"`) {
		t.Errorf("general tab should NOT show the priority list form")
	}
	if strings.Contains(out, `name="discord_webhook"`) {
		t.Errorf("general tab should NOT show notifications")
	}
	// The time-format selector lives on the general tab, beside the timezone
	// control, and posts to its own endpoint.
	for _, want := range []string{
		`name="timezone"`,
		`name="time_format"`,
		`action="/settings/time-format"`,
		`value="12"`,
		`value="24"`,
		`name="csrf_token"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("general tab time-format control missing %q", want)
		}
	}
}

// newTimeFormatDeps wires a settings page backed by a real sqlite store so a
// POST → store → re-render round trip can be asserted end to end.
func newTimeFormatDeps(t *testing.T) (*settingsDeps, *scs.SessionManager) {
	t.Helper()
	s, q := newTestSettings(t)
	tmpl, err := web.Templates()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}
	sm := scs.New()
	// The display clock is a process-wide singleton; restore it so one test
	// cannot leak its choice into another.
	prev := timeutil.Display.Format()
	t.Cleanup(func() { timeutil.Display.Set(prev) })
	displayClock = timeutil.Display
	return &settingsDeps{
		s:   s,
		q:   q,
		t:   tmpl,
		sm:  sm,
		loc: timeutil.NewZone(time.UTC),
	}, sm
}

// postTimeFormat submits the form and returns the recorder + flash context.
func postTimeFormatDo(t *testing.T, d *settingsDeps, sm *scs.SessionManager, body string) (*httptest.ResponseRecorder, context.Context) {
	t.Helper()
	req, ctx := loadSession(t, sm, formPost("/settings/time-format", body))
	rec := httptest.NewRecorder()
	d.postTimeFormat(rec, req)
	return rec, ctx
}

func TestPostTimeFormat_RoundTrip(t *testing.T) {
	d, sm := newTimeFormatDeps(t)
	ctx := context.Background()

	// Default: 24-hour everywhere.
	if got, _ := d.s.TimeFormat(ctx); got != store.TimeFormat24 {
		t.Fatalf("default time format = %q, want %q", got, store.TimeFormat24)
	}
	if timeutil.Display.Hour12() {
		t.Fatal("display clock should start 24-hour")
	}

	rec, _ := postTimeFormatDo(t, d, sm, "time_format=12")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST 12 → status %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/settings" {
		t.Errorf("redirect = %q, want /settings", loc)
	}
	if got, _ := d.s.TimeFormat(ctx); got != store.TimeFormat12 {
		t.Fatalf("stored time format = %q, want %q", got, store.TimeFormat12)
	}
	// Live swap: the running UI (and the client clock) flips with no restart.
	if !timeutil.Display.Hour12() {
		t.Fatal("display clock did not swap to 12-hour")
	}

	// The re-rendered general tab reflects the new state.
	out := renderGeneralTab(t, d, sm)
	if !strings.Contains(out, `<option value="12" selected>`) {
		t.Errorf("general tab should show 12-hour selected; got:\n%s", timeFormatSelect(out))
	}
	if strings.Contains(out, `<option value="24" selected>`) {
		t.Error("general tab should not also show 24-hour selected")
	}

	// And back to 24-hour.
	rec, _ = postTimeFormatDo(t, d, sm, "time_format=24")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST 24 → status %d, want 303", rec.Code)
	}
	if got, _ := d.s.TimeFormat(ctx); got != store.TimeFormat24 {
		t.Fatalf("stored time format = %q, want %q", got, store.TimeFormat24)
	}
	if timeutil.Display.Hour12() {
		t.Fatal("display clock did not swap back to 24-hour")
	}
	out = renderGeneralTab(t, d, sm)
	if !strings.Contains(out, `<option value="24" selected>`) {
		t.Errorf("general tab should show 24-hour selected; got:\n%s", timeFormatSelect(out))
	}
}

func TestPostTimeFormat_InvalidValueLeavesStateUntouched(t *testing.T) {
	d, sm := newTimeFormatDeps(t)
	ctx := context.Background()
	if err := d.s.SetTimeFormat(ctx, store.TimeFormat24); err != nil {
		t.Fatal(err)
	}

	rec, flashCtx := postTimeFormatDo(t, d, sm, "time_format=am%2Fpm")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("invalid value → status %d, want a 303 back to the form", rec.Code)
	}
	if got, _ := d.s.TimeFormat(ctx); got != store.TimeFormat24 {
		t.Fatalf("rejected value was persisted: %q", got)
	}
	if timeutil.Display.Hour12() {
		t.Fatal("rejected value swapped the live display clock")
	}
	if flash := sm.GetString(flashCtx, "flash"); flash != "flash.time_format_invalid" {
		t.Errorf("flash = %q, want flash.time_format_invalid", flash)
	}
}

func TestPostTimeFormat_FailedWriteNotReportedAsSuccess(t *testing.T) {
	s, q := brokenSettings(t)
	sm := scs.New()
	prev := timeutil.Display.Format()
	t.Cleanup(func() { timeutil.Display.Set(prev) })
	d := &settingsDeps{s: s, q: q, sm: sm}

	req, ctx := loadSession(t, sm, formPost("/settings/time-format", "time_format=12"))
	rec := httptest.NewRecorder()
	d.postTimeFormat(rec, req)

	if rec.Code == http.StatusSeeOther {
		t.Fatalf("failed save reported as success (303 redirect)")
	}
	if flash := strings.ToLower(sm.GetString(ctx, "flash")); strings.Contains(flash, "saved") {
		t.Fatalf("failed save left a success flash: %q", flash)
	}
	if timeutil.Display.Hour12() {
		t.Fatal("failed save still swapped the live display clock")
	}
}

// TestPostTimeFormat_OnUpdateCalled proves the settings-change hook still
// fires, so anything re-reading the setting on reload stays in sync.
func TestPostTimeFormat_OnUpdateCalled(t *testing.T) {
	d, sm := newTimeFormatDeps(t)
	calls := 0
	d.onUpdate = func() { calls++ }
	postTimeFormatDo(t, d, sm, "time_format=12")
	if calls != 1 {
		t.Fatalf("onUpdate called %d times, want 1", calls)
	}
}

// TestPostTimeFormat_LayoutFollowsTheSetting proves the shared time layout
// strings (used by the drops/history/dashboard call sites) flip with the
// setting — i.e. the helpers are actually wired to the live clock.
func TestPostTimeFormat_LayoutFollowsTheSetting(t *testing.T) {
	d, sm := newTimeFormatDeps(t)
	loc := timeutil.NewZone(time.UTC)
	ts := time.Date(2026, 9, 28, 15, 4, 5, 0, time.UTC)

	if got, want := timeutil.FormatDateTime(ts, loc.Location()), "2026-09-28 15:04 UTC"; got != want {
		t.Errorf("24h date+time = %q, want %q", got, want)
	}
	postTimeFormatDo(t, d, sm, "time_format=12")
	if got, want := timeutil.FormatDateTime(ts, loc.Location()), "2026-09-28 3:04 PM UTC"; got != want {
		t.Errorf("12h date+time = %q, want %q", got, want)
	}
	if got, want := timeutil.FormatClock(ts, loc.Location()), "3:04:05 PM"; got != want {
		t.Errorf("12h clock = %q, want %q", got, want)
	}
}

// renderGeneralTab renders the settings general tab through the real handler
// so the assertion covers renderTab's store read, not just the template.
func renderGeneralTab(t *testing.T, d *settingsDeps, sm *scs.SessionManager) string {
	t.Helper()
	req, _ := loadSession(t, sm, httptest.NewRequest("GET", "/settings", nil))
	rec := httptest.NewRecorder()
	d.renderTab(rec, req, "settings")
	if rec.Code != http.StatusOK {
		t.Fatalf("render general tab: status %d, body:\n%s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// timeFormatSelect trims the rendered page down to the select element so a
// failing assertion prints something readable.
func timeFormatSelect(page string) string {
	i := strings.Index(page, `<select name="time_format">`)
	if i < 0 {
		return "(no time_format select rendered)"
	}
	rest := page[i:]
	if j := strings.Index(rest, "</select>"); j >= 0 {
		return rest[:j+len("</select>")]
	}
	return rest
}

func TestSettingsTabs_PrioritySection(t *testing.T) {
	out := renderSettingsTab(t, "priority", settingsPageData{})
	if !strings.Contains(out, `action="/settings/global-games"`) {
		t.Errorf("priority tab should show the global priority list form")
	}
	if !strings.Contains(out, `name="priority_mode"`) {
		t.Errorf("priority tab should show the priority mode selector")
	}
	if !strings.Contains(out, `action="/settings/priority-mode"`) {
		t.Errorf("priority mode posts to its own endpoint")
	}
}

func TestSettingsTabs_NotificationsSection(t *testing.T) {
	out := renderSettingsTab(t, "notifications", settingsPageData{})
	if !strings.Contains(out, `name="discord_webhook"`) {
		t.Errorf("notifications tab should show the webhook field")
	}
	if !strings.Contains(out, `action="/settings/notifications"`) {
		t.Errorf("notifications form posts to /settings/notifications")
	}
}

func TestSettingsTabs_SecuritySection(t *testing.T) {
	out := renderSettingsTab(t, "security", settingsPageData{OIDC: settingsOIDC{Enabled: false}})
	if !strings.Contains(out, "Single sign-on") {
		t.Errorf("security tab should show the SSO card")
	}
	if !strings.Contains(out, `action="/settings/password"`) {
		t.Errorf("security tab should show the password form")
	}
}

// TestSettingsTabs_HealthSection verifies the Health tab renders canary
// results, "not configured" for unconfigured platforms, the settings form,
// and the Run-now HTMX button.
func TestSettingsTabs_HealthSection(t *testing.T) {
	out := renderSettingsTab(t, "health", settingsPageData{
		CanaryTwitch: canaryView{
			Configured: true,
			OK:         true,
			Detail:     "",
			When:       "2m ago",
		},
		CanaryKick:          canaryView{Configured: false},
		CanaryTwitchChannel: "somestreamer",
		CanaryKickChannel:   "",
		CanaryIntervalSec:   300,
	})

	// Twitch OK result shows success indicator
	if !strings.Contains(out, "canary-ok") {
		t.Errorf("health tab should show canary-ok class for a passing twitch result")
	}
	// Twitch "when" is shown
	if !strings.Contains(out, "2m ago") {
		t.Errorf("health tab should show the 'when' timestamp for twitch")
	}
	// Kick not configured
	if !strings.Contains(out, "not configured") {
		t.Errorf("health tab should show 'not configured' for unconfigured kick")
	}
	// Settings form fields present
	if !strings.Contains(out, `name="canary_twitch_channel"`) {
		t.Errorf("health tab should show canary_twitch_channel input")
	}
	if !strings.Contains(out, `name="canary_kick_channel"`) {
		t.Errorf("health tab should show canary_kick_channel input")
	}
	if !strings.Contains(out, `name="canary_interval_sec"`) {
		t.Errorf("health tab should show canary_interval_sec input")
	}
	// Form posts to /settings/canary
	if !strings.Contains(out, `action="/settings/canary"`) {
		t.Errorf("health tab canary form should post to /settings/canary")
	}
	// Run-now HTMX control
	if !strings.Contains(out, `hx-post="/settings/canary/run"`) {
		t.Errorf("health tab should have Run-now hx-post control")
	}
	if !strings.Contains(out, `hx-target="#canary-panel"`) {
		t.Errorf("health tab Run-now should target #canary-panel")
	}
	// canary-panel id present for fragment swap target
	if !strings.Contains(out, `id="canary-panel"`) {
		t.Errorf("health tab should have id=canary-panel for htmx swap")
	}
}

// TestCanaryPanelFragment verifies the canary_panel template can be rendered
// stand-alone (the fragment path used by canaryRun).
func TestCanaryPanelFragment(t *testing.T) {
	t.Helper()
	tmpl, err := web.Templates()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}
	var buf bytes.Buffer
	err = tmpl.ExecuteTemplate(&buf, "canary_panel", templateData{
		CSRFToken: "testtoken",
		Active:    "health",
		Page: settingsPageData{
			CanaryTwitch: canaryView{Configured: true, OK: false, Detail: "beacon timeout", When: "1m ago"},
			CanaryKick:   canaryView{Configured: true, OK: true, When: "30s ago"},
		},
	})
	if err != nil {
		t.Fatalf("render canary_panel: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "canary-err") {
		t.Errorf("canary_panel fragment: twitch failure should show canary-err class")
	}
	if !strings.Contains(out, "beacon timeout") {
		t.Errorf("canary_panel fragment: twitch detail should be in output")
	}
	if !strings.Contains(out, "canary-ok") {
		t.Errorf("canary_panel fragment: kick ok should show canary-ok class")
	}
	if !strings.Contains(out, `hx-post="/settings/canary/run"`) {
		t.Errorf("canary_panel fragment: Run-now button missing")
	}
}

// unlinkedCardIs reports whether the rendered priority tab contains the
// global unlinked-mining card, and with which hidden `enabled` value.
func unlinkedCardIs(t *testing.T, page settingsPageData) (present bool, hidden string) {
	t.Helper()
	out := renderSettingsTab(t, "priority", page)
	form := `action="/settings/mine-unlinked-global"`
	i := strings.Index(out, form)
	if i < 0 {
		return false, ""
	}
	rest := out[i:]
	// Hidden `enabled` input is the first one after the form's action.
	j := strings.Index(rest, `name="enabled"`)
	if j < 0 {
		return true, ""
	}
	rest = rest[j:]
	k := strings.Index(rest, `value="`)
	if k < 0 {
		return true, ""
	}
	v := rest[k+len(`value="`):]
	return true, v[:strings.Index(v, `"`)]
}

// TestSettingsTabs_PriorityUnlinkedCard proves the priority tab renders the
// global unlinked-mining card wired to its own endpoint, with the hidden
// `enabled` value flipped so the button submits the opposite of the state.
func TestSettingsTabs_PriorityUnlinkedCard(t *testing.T) {
	present, hidden := unlinkedCardIs(t, settingsPageData{MineUnlinkedGlobal: false})
	if !present {
		t.Fatalf("priority tab should render the global unlinked card")
	}
	if hidden != "1" {
		t.Errorf("card OFF should post enabled=1 to switch on, got %q", hidden)
	}
	// The card must also carry the OR-semantics hint so the operator knows a
	// per-account opt-in is never suppressed.
	if !strings.Contains(renderSettingsTab(t, "priority", settingsPageData{}), "Force unlinked mining for all accounts") {
		t.Errorf("card should include the global override semantics hint")
	}

	present, hidden = unlinkedCardIs(t, settingsPageData{MineUnlinkedGlobal: true})
	if !present {
		t.Fatalf("card should render when ON too")
	}
	if hidden != "0" {
		t.Errorf("card ON should post enabled=0 to switch off, got %q", hidden)
	}
}

// TestPostMineUnlinkedGlobal_Persists proves the toggle persists to the
// settings store, re-spins the scheduler (watchers snapshot the value at
// build time), flashes, and redirects back to /priority.
func TestPostMineUnlinkedGlobal_Persists(t *testing.T) {
	s, q := newTestSettings(t)
	sm := scs.New()
	reloads := 0
	d := &settingsDeps{s: s, q: q, sm: sm, reload: func(context.Context) error {
		reloads++
		return nil
	}}

	// Enable.
	req, ctx := loadSession(t, sm, formPost("/settings/mine-unlinked-global", "enabled=1"))
	rec := httptest.NewRecorder()
	d.postMineUnlinkedGlobal(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/priority" {
		t.Errorf("expected redirect to /priority, got %q", loc)
	}
	if got, _ := s.MineUnlinkedGlobal(ctx); !got {
		t.Fatalf("global override should be persisted on after enabling")
	}
	if reloads != 1 {
		t.Errorf("watchers should be reloaded so the toggle applies live, got %d reloads", reloads)
	}
	if got := sm.GetString(ctx, "flash"); got != "flash.mine_unlinked_global_enabled" {
		t.Errorf("unexpected flash after enabling: %q", got)
	}

	// The tab re-renders in the new state (this is what renderTab feeds the
	// template: the store read above).
	on := settingsPageData{MineUnlinkedGlobal: true}
	if present, hidden := unlinkedCardIs(t, on); !present || hidden != "0" {
		t.Errorf("re-render after enable should show enabled=0, got present=%v hidden=%q", present, hidden)
	}

	// Disable.
	req, ctx = loadSession(t, sm, formPost("/settings/mine-unlinked-global", "enabled=0"))
	rec = httptest.NewRecorder()
	d.postMineUnlinkedGlobal(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect on disable, got %d", rec.Code)
	}
	if got, _ := s.MineUnlinkedGlobal(ctx); got {
		t.Fatalf("global override should be off after disabling")
	}
	if got := sm.GetString(ctx, "flash"); got != "flash.mine_unlinked_global_disabled" {
		t.Errorf("unexpected flash after disabling: %q", got)
	}
	if present, hidden := unlinkedCardIs(t, settingsPageData{}); !present || hidden != "1" {
		t.Errorf("re-render after disable should show enabled=1, got present=%v hidden=%q", present, hidden)
	}
}
