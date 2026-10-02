# Resolving a game's canonical Twitch directory slug

Research note — **not implemented**. Written 2026-10-02 while reviewing the
`DirectoryGameRedirect` slug-resolution work (`internal/platform/twitch/channels.go`).

Status of that work: `resolveGameSlug` / `directorySlugResolved` are in the tree
and tested, but see "Response shape: confirmed, not a stable contract" below — and, more
importantly, **"The bigger bug"**: renaming breaks whitelist matching *before* the
slug is ever used, so fixing the slug alone does not make these games work.

## The problem

Two distinct failure classes, same user-visible symptom ("no eligible streams
live", or the game simply never appearing). They need different fixes.

**Class 1 — the display name never matched the slug.** Twitch's name and slug
have simply never agreed. Known instance: `CONTROL Resonant` is and always was
named that, while its slug is and always was `control-2`. No rename is involved;
the local derivation just can never produce `control-2`. This is what the shipped
`directorySlugResolved` fixes.

**Class 2 — Twitch renamed the game and kept the slug.** The display name changes,
the `/directory/category/<slug>` path segment does not. Known instances:

- `Overwatch 2` → `Overwatch` (renamed earlier in 2026), slug stayed `overwatch-2`
- `Tom Clancy's Rainbow Six Siege` → `Rainbow Six Siege` (date unknown), slug
  stayed `tom-clancys-rainbow-six-siege`

Class 2 is **not** fixed by slug resolution alone. Adding either old name to Drop
Priority yields nothing, because the whitelist is matched against the operator's
typed string while Twitch reports the new name. See "The bigger bug" below — that
is the live, reproducible case.

Local derivation (`internal/gameslug.Slug`) cannot fix class 1 by construction.
`DevilXD/TwitchDropsMiner` upstream has the identical bug and does not handle
renames either: `utils.py`'s `Game.slug` is a `cached_property` that slugifies
the *name*, and `twitch.py get_live_streams` feeds that derived slug straight into
`GQL_QUERIES["GameDirectory"]`, where failure is fatal. Notably, upstream defines
a `SlugRedirect` operation in `constants.py` and **never calls it** — dead code.

## Response shape: confirmed, but not a stable contract

Our implementation decodes `{"data":{"game":{"slug":"..."}}}` from
`DirectoryGameRedirect`. **That path is independently confirmed by a working
implementation** — [`twitch-gql-rs` 0.3.5](https://docs.rs/crate/twitch-gql-rs/latest/source/src/gql.rs)
calls the operation with the same hash and the same variable, and extracts exactly
that path:

```rust
let gql = GQLOperation::new("DirectoryGameRedirect")
    .with_extensions("1f0300090caceec51f33c5e20647aceff9017f740f223c3c532ba6fa59f6b6cc")
    .with_variables(json!({ "name": game_name }));
let gql: Value = gql.json().await?;
let slug = get_value_from_vec(gql, &["data", "game", "slug"])?;
```

Confirmed: operation name, hash, `{"name": game_name}` variable, and response
path `data.game.slug`. The implementation also expects a JSON string, erroring
with `SlugError::GameSlugParsingFailed` otherwise — which matches our decoder
treating empty/non-string as "no answer".

Supporting detail, for what each source does and does not establish:

- `twitch-gql-rs` — strict confirmation: single path, `data.game.slug`, string-typed.
- `fireph/TwitchDropsFarmer` — calls the same operation but tries `gameDirectory`,
  then `game`, then `directoryGame`, and dumps available keys on failure. Weaker:
  it shows the operation works, not that `game` is the only path.
- `DevilXD` / `rangermix` — ship the hash and the `name` variable in constants,
  but never call it (dead code upstream).

The residual risk is **contract stability, not shape**: this is an internal
persisted GraphQL operation with no public Twitch documentation, so Twitch could
rotate the hash or change the field. That is materially weaker than "shape
unknown", but it is the same class of risk as every other hash in `ops.go` —
which already carries the note "refresh these constants if production sees
PersistedQueryNotFound errors".

Consequence for our code: the current decoder is fine as written. The earlier
suggestion to accept `gameDirectory` / `directoryGame` as alternates is dropped —
it was only justified by the mistaken belief that the shape was unconfirmed, and
guessing at extra keys would make the decoder worse, not more robust.

## Ranked options

| # | Option | Rename-proof | Cost | Confidence |
|---|--------|--------------|------|------------|
| 1 | `GameByID` by numeric game ID | Yes — keyed on immutable ID | ~1 call per new game, then cached | **Verified**, shipped in production |
| 2 | `BrowsePage_AllDirectories` bulk snapshot | Yes, bulk | **Unauthenticated** | **Verified**, 3 production impls |
| 3 | Keep `DirectoryGameRedirect` | Name-keyed only | ~1 call per game | Shape confirmed (twitch-gql-rs); contract may drift |
| 4 | Capture `slug` from `OpGameDirectory` responses | No | **Zero** extra calls | **Verified** response shape |

### 1. `GameByID` — the actual fix

```graphql
query GameByID($id: ID!) { game(id: $id) { slug } }
```

Shipped in non-test code by [Guliveer/twitch-miner-go](https://github.com/Guliveer/twitch-miner-go)
with public request-builder and response structs. Keyed on the game's immutable
ID, so it is *structurally* immune to renames — not "self-healing after a
rename", but "renames are not observable". Also immune to casing, punctuation
and non-ASCII games where slugification diverges from Twitch.

Reference implementation: `internal/gql/operations.go` (`GetGameSlug`),
`internal/model/game_registry.go` (thread-safe `gameID → slug` registry),
`internal/twitch/client.go:316-330` (lazy resolve-and-register).

**We already decode the ID and discard it:** `campaigns.go:70` reads
`Game.ID string \`json:"id"\`` from `ViewerDropsDashboard`, and the
`platform.Campaign` literal at `campaigns.go:258` uses only `c.Game.DisplayName`.
`platform.Campaign` (`internal/platform/types.go:39-82`) has no field to carry it.

### 2. `BrowsePage_AllDirectories` — bulk name+slug+id map

```json
{"operationName":"BrowsePage_AllDirectories",
 "variables":{"limit":N,"options":{"sort":"VIEWER_COUNT","tags":[]}},
 "extensions":{"persistedQuery":{"version":1,
   "sha256Hash":"2f67f71ba89f3c0ed26a141ec00da1defecb2303595f5cda4298169549783d9e"}}}
```

Response carries `data.directoriesWithTags.edges[].node` with
`{id, name, slug, displayName, viewersCount, avatarURL, tags, originalReleaseDate}`.

Verified unauthenticated — [glance](https://github.com/glanceapp/glance) sends
only a `Client-ID` header; [supibot](https://github.com/Supinic/supibot) only a
`Referer`. So it costs nothing against the authenticated-session budget that
GrubDrops is careful to protect.

Useful to prewarm the ID→slug cache, to cross-validate options 1 and 3, and to
surface rename events (same ID, changed `displayName`) for logging.

Max page size unverified (10 and 24 observed); full cursor semantics unverified.

### 4. Free slug capture — zero new API calls

- `DirectoryPage_Game` responses contain `game{ id, name, displayName, slug }`
  inside each stream node (public query text, twitch-miner-go). We decode only
  `game{id, displayName}` at `channels.go:150-153` — we discard the slug from a
  call we already make. Every successful directory query can register the true
  slug for that game ID.
- The sidecar DOM scrape finds `a[href*="/directory/category/"]` at
  `internal/auth/browser/sidecar/twitch.go:670` but reads only `.textContent`
  (line 672). The canonical slug is in that href.

## What will not work

- **Reading the slug off the campaign.** Twitch's drops `game` object is
  `{id, name, boxArtURL, __typename}` — no slug. Confirmed against a captured
  `Inventory` fixture and three independent clients, one of which types
  `Slug *string` (a pointer) because it is absent from some responses. Worse, our
  client sends persisted-hash-only with no inline query, so the selection set is
  fixed server-side by the hash — not even selectable without switching to raw
  queries (`gqlQuery` exists at `client.go:296`, but see the integrity caveat).
- **Twitch's public web pages.** `/directory/category/<slug>` is a
  client-rendered SPA route with no server-rendered slug list. Treat
  "Twitch 301-redirects an old slug to the new one" as speculation — our own
  field comment records the opposite (a wrong slug returns no game, no redirect).
  `/search` returns channels, not games. FrankerFaceZ shipping a *static
  build-time* game list is itself evidence no convenient public source exists.
- **Operation names with no evidence of existing:** `DirectoryPage_AllDirectories`,
  `AllDirectories`, `SearchResultsSearchQuery`, `SearchPage_GameSearch`,
  `ChannelSearch`, `BrowsePage_Games`, `SearchBox_SearchQuery`. Zero GitHub hits
  for each. Do not build against them.

## The bigger bug: whitelist matching is exact-name equality

Found while investigating class 2, verified by reading code (not inferred).
**This is why adding `Overwatch 2` or `Tom Clancy's Rainbow Six Siege` to Drop
Priority finds nothing** — and `directorySlugResolved` cannot fix it, because it
runs *after* the gate that already rejected the game.

### Every gate is exact lowercased string equality

The whitelist contributes two tokens per row — `games.name` and `games.slug`,
both lowercased (`internal/discovery/discovery.go:83-90`, `:102-109`). The
predicate is set membership:

```go
// internal/discovery/twitch.go:130-144
func buildAllowList(whitelist []string) func(string) bool {
	set := make(map[string]struct{}, len(whitelist))
	for _, g := range whitelist {
		set[strings.ToLower(strings.TrimSpace(g))] = struct{}{}
	}
	return func(game string) bool {
		_, ok := set[strings.ToLower(strings.TrimSpace(game))]
		return ok
	}
}
```

The watcher's per-account gate (`cmd/miner/main.go:967-979`) matches either token.
Applied at `internal/watcher/watcher.go:1380`, `:634`,
`internal/scheduler/discovery.go:99`, and as a detail-fetch gate at
`internal/platform/twitch/campaigns.go:243`. There are **five** independent
name-equality gates; a rename breaks all of them.

| user typed | tokens in the set | Twitch reports | match? |
|---|---|---|---|
| `Overwatch 2` | `{overwatch 2, overwatch-2}` | `Overwatch` | no |
| `Tom Clancy's Rainbow Six Siege` | `{tom clancy's rainbow six siege, tom-clancys-rainbow-six-siege}` | `Rainbow Six Siege` | no |

The apostrophe survives in the name token (only `TrimSpace`, no character
folding) while the derived slug drops it — so even those two tokens disagree with
each other. `gameslug.Slug()` is never applied to the incoming campaign name at
any gate, and since Twitch reports by `displayName` the `games.slug` token is
dead weight here (it only helps a backend reporting games *by slug*).

### What gets stored, and why it never self-heals

All three add handlers write the operator's raw text verbatim plus a locally
derived slug (`handlers_drops.go:897-911`, `handlers_settings.go:598-611`,
`handlers_accounts.go:276-291`):

```go
name := strings.TrimSpace(r.FormValue("name"))
slug := gameslug.Slug(name)
gameID := gameslug.ID(name)
d.q.UpsertGame(ctx, gen.UpsertGameParams{ID: gameID, Name: name, Slug: slug, Priority: 0})
```

`UpsertGame` updates only on an **id** collision
(`internal/store/queries/games.sql:4-7`: `ON CONFLICT(id) DO UPDATE SET name =
excluded.name, slug = excluded.slug`), and the auto-upsert on every persisted
campaign derives its id from the *incoming* name
(`internal/store/campaign_persister.go:68-75`). A canonical Twitch name therefore
yields a **different id** (`g_overwatch` vs `g_overwatch_2`), so `DO UPDATE` never
fires. The whitelist row is never corrected; a second orphan `games` row is
inserted instead, and its error is swallowed (`_ =` at `campaign_persister.go:69`).

### The TV path has the association and discards it

`chandisc.go:135-146` walks `sess.Games` (whitelist names), resolves the slug,
queries the directory, then calls AvailableDrops per channel. The campaigns coming
back are built at `chandisc.go:160-171` with `Game: vc.Game.Name` — Twitch's own
display name. The whitelist entry that drove the walk is **never re-attached**:
`backend.go:257-269` returns the slice verbatim, and `GameFilter` appears nowhere
under `chandisc.go` or `browser_backend.go`. One wasted variable would fix this.

### Untested hazard: the Overwatch seed collides with the slug UNIQUE index

`migrations/0005_more_games.sql:6` seeds
`('g_overwatch', 'Overwatch 2', 'overwatch-2', 100)`. Adding `Overwatch 2` where
that row is intact computes `gameID = g_overwatch_2` (no id collision) but
`slug = overwatch-2` (**already taken**), and `ON CONFLICT(id)` does not
intercept a different unique index — so it should raise `UNIQUE constraint failed:
games.slug` and surface as a bare 500 from `handlers_drops.go:908-911`.
Conversely, persisting Twitch's `Overwatch` hits `g_overwatch`, colliding with the
seed id, so `DO UPDATE` rewrites the seed row to `name='Overwatch'` — freeing
`overwatch-2` and letting a retry succeed into the same broken state, silently.

**Not verified by execution** — the SQL is unambiguous but this was not run
against a live SQLite. Worth a short repro: it decides whether the Overwatch case
is one bug or two.

### Fix options (not implemented)

| Option | Change | Covers | Notes |
|---|---|---|---|
| **C** | `chandisc.go:162` — use the loop variable that drove the walk, or carry both names | TV path | One line. But `campaigns.game` is also shown on `/drops`, so the UI would display the operator's stale name |
| **B** | Migration `0016` + `canonical_name`/`canonical_slug`, written by the persister | both paths + UI | **Bootstraps circularly** unless the resolved slug is written under the id the operator actually whitelisted |
| **D** | Add `GameID` to `platform.Campaign` from the already-decoded-then-discarded `c.Game.ID` (`campaigns.go:70`); match on it | both paths | Most correct; needs a backfill, inherits B's bootstrapping problem |
| **A** | Add `gameslug.Slug(games.name)` as a third token; feed the resolved slug to the predicate | both paths | Smallest diff, no migration — but turns the predicate into I/O in a hot loop (`watcher.go:634` runs per campaign per tick) |

Reading: **C first** (one line, fixes TV immediately, association provably already
exists), then **B re-scoped**. A and D both presuppose that step.

## Gotchas

- **Never send `Client-Integrity` with a raw (non-persisted) query.** Documented
  cause of `"service error"` for some categories in twitch-miner-go. Consistent
  with our own AGENTS.md rule. Raw queries do not consume the integrity path, so
  they do not contend with `ViewerDropsDashboard`.
- **Raw queries are an unofficial path.** Twitch accepts them (wundergraph's
  analysis of twitch.tv confirms arbitrary non-persisted operations), and
  twitch-miner-go uses three in production, but Twitch could tighten it. Slightly
  riskier than a persisted query.
- **`DirectoryPage_Game` is slug-only** (`$slug: String!, $first: Int!, $after:
  Cursor, $options: GameStreamOptions`) — no numeric-ID variant. Persisted-query
  hashes pin the variable set, so an older revision took `name` instead of `slug`
  (TychoTheTaco hash `df4bb6cc…`). Do not mix hashes and variable shapes.
- **`chandisc.listByChannels` has no game ID.** It iterates display-name strings
  from `sess.Games` (`chandisc.go:137`), so an ID-keyed resolver needs
  `DirectoryRoot_Directory` (name→ID, hash `9f4f6ae6…`, verified unauthenticated)
  chained into `GameByID`.

## Suggested rollout order

1. `GameByID` keyed on `campaign.game.id` — primary. Requires widening
   `platform.Campaign` with a `GameID` field and threading it to the call sites
   at `backend.go:414`, `browser_backend.go:414`, `chandisc.go:137`.
2. `BrowsePage_AllDirectories` — unauthenticated prewarm + cross-validation.
3. Keep `DirectoryGameRedirect` as fallback (shape confirmed; hash rot is the risk).
4. Capture `slug` from existing `OpGameDirectory` responses — free maintenance.
5. `gameslug.Slug()` — degraded-mode guess only, which is how it is already
   documented in `channels.go`.

With (1) in place, `gameslug.Slug()` stops being load-bearing for correctness.

## Persistence (if we ever want it)

- `games.slug` column — `migrations/0001_init.sql:71`, written via
  `UpsertGame` (`queries/games.sql:4-7`), which already overwrites on conflict.
  Today all four writers pass `gameslug.Slug(name)`. The column is currently
  dead weight for directory lookups — every read is just an extra lowercased
  whitelist token. Caveat: `slug TEXT NOT NULL UNIQUE`, so a collision aborts
  the upsert.
- `kv` table — `queries/settings.sql:1-12` (`UpsertSettingString`), natural for
  `slug:<name>`.
- `campaigns.raw_json` — `campaign_persister.go:102-118` already serializes
  `allowed_channels` there; a `game_id` key would fit with no migration.

Note the existing cache comment at `channels.go:104-108` argues *against*
persisting slugs: a persisted or TTL'd slug serves a stale category across a
rename instead of self-healing on restart. ID-keyed caching keeps that property.

## Separate problem: Kick

Not addressed by anything above. `kick/api.go:530-542` (`gameName`) collapses a
nested `game.slug` into the display-name field, so Kick's category identity is
already lossy at parse time. `ChannelLivestream` decodes `categories[].slug` and
returns only `.Name` (`api.go:255-278`) — the same one-line discard. No Kick slug
resolver exists (no redirect op, no directory query).

## Sources

- [twitch-gql-rs 0.3.5 `src/gql.rs`](https://docs.rs/crate/twitch-gql-rs/latest/source/src/gql.rs) — working `DirectoryGameRedirect` caller; confirms hash, `name` variable and `data.game.slug`
- [DevilXD constants.py](https://github.com/DevilXD/TwitchDropsMiner/blob/master/constants.py) · [utils.py](https://github.com/DevilXD/TwitchDropsMiner/blob/master/utils.py) · [issue #943](https://github.com/DevilXD/TwitchDropsMiner/issues/943)
- [rangermix operations.py](https://github.com/rangermix/TwitchDropsMiner/blob/main/src/config/operations.py)
- [fireph operations.go](https://github.com/fireph/TwitchDropsFarmer/blob/main/internal/twitch/operations.go) · [graphql.go](https://github.com/fireph/TwitchDropsFarmer/blob/main/internal/twitch/graphql.go) · [graphql_types.go](https://github.com/fireph/TwitchDropsFarmer/blob/main/internal/twitch/graphql_types.go)
- [Guliveer constants.go](https://github.com/Guliveer/twitch-miner-go/blob/main/internal/constants/constants.go) · [gql/client.go](https://github.com/Guliveer/twitch-miner-go/blob/main/internal/gql/client.go) · [gql/operations.go](https://github.com/Guliveer/twitch-miner-go/blob/main/internal/gql/operations.go) · [model/game_registry.go](https://github.com/Guliveer/twitch-miner-go/blob/main/internal/model/game_registry.go) · [twitch/client.go](https://github.com/Guliveer/twitch-miner-go/blob/main/internal/twitch/client.go)
- [glance widget-twitch-top-games.go](https://github.com/glanceapp/glance/blob/main/internal/glance/widget-twitch-top-games.go) · [supibot topgames](https://github.com/Supinic/supibot/blob/master/commands/topgames/index.ts) · [Roku GetCategories2.brs](https://github.com/worldreboot/twitch-reloaded-roku/blob/master/components/GetCategories2.brs)
- [TychoTheTaco twitch.ts](https://github.com/TychoTheTaco/Twitch-Drops-Bot/blob/dev/src/twitch.ts) · [Inventory fixture](https://github.com/TychoTheTaco/Twitch-Drops-Bot/blob/dev/test/data/Inventory/0.json)
- [Alorf TwitchDropsBot Postman collection](https://github.com/Alorf/TwitchDropsBot/blob/master/TwitchDropsBot.Core/Postman/Twitch.postman_collection.json)
- [wundergraph: Twitch GQL analysis](https://wundergraph.com/blog/graphql_in_production_analyzing_public_graphql_apis_1_twitch_tv)
- [FrankerFaceZ twitch-twilight](https://github.com/FrankerFaceZ/FrankerFaceZ/blob/master/src/sites/twitch-twilight/index.js)
