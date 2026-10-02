package twitch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/JourneyDocker/grubdrops/internal/gameslug"
	"github.com/JourneyDocker/grubdrops/internal/platform"
)

// slugServer serves DirectoryGameRedirect from slugs (game name ->
// canonical slug) and 500s everything else. It also records how many
// redirect calls were made, so cache tests can assert single-flight.
func slugServer(t *testing.T, slugs map[string]string, calls *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req gqlRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, OpSlugRedirect.Name, req.OperationName)
		if calls != nil {
			*calls++
		}
		name, _ := req.Variables["name"].(string)
		slug, ok := slugs[name]
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"game":{"slug":` + toJSONString(slug) + `}}}`))
	}))
}

func toJSONString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// TestDirectorySlugResolved_CanonicalSlugs covers the path callers
// actually use: the display name goes out to Twitch and Twitch's slug
// comes back verbatim. Two different reasons the display name cannot
// produce the slug: "Overwatch" was renamed from "Overwatch 2" and kept
// overwatch-2, while "CONTROL Resonant" was never renamed and simply
// never matched its control-2 slug. Neither is guessable locally.
func TestDirectorySlugResolved_CanonicalSlugs(t *testing.T) {
	slugs := map[string]string{
		"Overwatch":                         "overwatch-2",
		"CONTROL Resonant":                  "control-2",
		"Warhammer 40,000: Space Marine II": "warhammer-40-000-space-marine-ii",
		"Apex Legends":                      "apex-legends",
		"Counter-Strike 2":                  "counter-strike-2",
	}
	srv := slugServer(t, slugs, nil)
	defer srv.Close()

	ch := &channels{c: newTestClient(srv.URL)}
	ctx := context.Background()
	sess := platform.Session{AccessToken: "tok"}
	for game, want := range slugs {
		assert.Equal(t, want, ch.directorySlugResolved(ctx, sess, game),
			"directorySlugResolved(%q)", game)
	}
}

func TestResolveGameSlug_FailureFallback(t *testing.T) {
	// Unknown game -> server 500s -> resolver errors -> resolved
	// lookup falls back to the local derivation.
	srv := slugServer(t, map[string]string{}, nil)
	defer srv.Close()

	ch := &channels{c: newTestClient(srv.URL)}
	_, err := ch.resolveGameSlug(context.Background(), platform.Session{AccessToken: "tok"}, "Apex Legends")
	require.Error(t, err)

	got := ch.directorySlugResolved(context.Background(), platform.Session{AccessToken: "tok"}, "Apex Legends")
	assert.Equal(t, gameslug.Slug("Apex Legends"), got)
}

func TestResolveGameSlug_EmptySlug(t *testing.T) {
	srv := slugServer(t, map[string]string{"Some Game": ""}, nil)
	defer srv.Close()

	ch := &channels{c: newTestClient(srv.URL)}
	_, err := ch.resolveGameSlug(context.Background(), platform.Session{AccessToken: "tok"}, "Some Game")
	require.Error(t, err)

	got := ch.directorySlugResolved(context.Background(), platform.Session{AccessToken: "tok"}, "Some Game")
	assert.Equal(t, gameslug.Slug("Some Game"), got)
}

func TestResolveGameSlug_CachesSuccess(t *testing.T) {
	var calls int
	srv := slugServer(t, map[string]string{"CONTROL Resonant": "control-2"}, &calls)
	defer srv.Close()

	ch := &channels{c: newTestClient(srv.URL)}
	ctx := context.Background()
	sess := platform.Session{AccessToken: "tok"}
	first, err := ch.resolveGameSlug(ctx, sess, "CONTROL Resonant")
	require.NoError(t, err)
	second, err := ch.resolveGameSlug(ctx, sess, "CONTROL Resonant")
	require.NoError(t, err)
	assert.Equal(t, first, second)
	assert.Equal(t, 1, calls, "second resolve should hit the cache")
}
