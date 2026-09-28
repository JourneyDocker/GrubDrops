package twitch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aalejandrofer/grubdrops/internal/platform"
)

// fakeGQL routes persisted ops by operationName.
func fakeGQL(t *testing.T, byOp map[string]func(vars map[string]any) string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
		}
		require.NoError(t, json.Unmarshal(raw, &req))
		f, ok := byOp[req.OperationName]
		if !ok {
			_, _ = w.Write([]byte(`{"data":{}}`))
			return
		}
		_, _ = w.Write([]byte(f(req.Variables)))
	}))
}

const dirRust = `{"data":{"game":{"streams":{"edges":[
 {"node":{"id":"s1","viewersCount":10,"broadcaster":{"id":"c1","login":"alpha"},"game":{"id":"263490","displayName":"Rust"}}},
 {"node":{"id":"s2","viewersCount":5,"broadcaster":{"id":"c2","login":"beta"},"game":{"id":"263490","displayName":"Rust"}}}]}}}}`

const availAlpha = `{"data":{"channel":{"viewerDropCampaigns":[{"id":"campA","name":"Rust Isles General","game":{"id":"263490","name":"Rust"},"endAt":"2030-01-01T00:00:00Z",
 "timeBasedDrops":[
  {"id":"dWatch","name":"Box","requiredMinutesWatched":60,"requiredSubs":0,"benefitEdges":[{"benefit":{"id":"bW","name":"Box","imageAssetURL":"http://img/b.png"}}]},
  {"id":"dSub","name":"Sub Badge","requiredMinutesWatched":30,"requiredSubs":1,"benefitEdges":[{"benefit":{"id":"bS","name":"Badge","imageAssetURL":""}}]}]}]}}}`

const inventoryTV = `{"data":{"currentUser":{"inventory":{"dropCampaignsInProgress":[
 {"id":"campB","name":"Rust Isles Tac Gloves","game":{"id":"263490","name":"Rust"},"endAt":"2030-01-01T00:00:00Z",
  "allow":{"channels":[{"id":"c9","name":"welyn"}]},
  "timeBasedDrops":[{"id":"dG","name":"Gloves","requiredMinutesWatched":60,"requiredSubs":0,
    "benefitEdges":[{"benefit":{"id":"bG","name":"Gloves","imageAssetURL":""}}],
    "self":{"currentMinutesWatched":14,"isClaimed":false,"dropInstanceID":"i1"}}]}],
 "gameEventDrops":[]}}}}`

func TestListByChannels_TVSession(t *testing.T) {
	srv := fakeGQL(t, map[string]func(map[string]any) string{
		"DirectoryPage_Game": func(v map[string]any) string {
			if v["slug"] == "rust" {
				return dirRust
			}
			return `{"data":{"game":{"streams":{"edges":[]}}}}` // Review Focus #3
		},
		"DropsHighlightService_AvailableDrops": func(v map[string]any) string {
			if v["channelID"] == "c1" {
				return availAlpha
			}
			return `{"data":{"channel":{"viewerDropCampaigns":[]}}}`
		},
		"Inventory": func(map[string]any) string { return inventoryTV },
	})
	defer srv.Close()

	b := newForTest(srv.URL)
	sess := platform.Session{AccessToken: "tv", ClientID: ClientTV, Games: []string{"Rust", "Offline Game"}}
	camps, err := b.ListActiveCampaigns(context.Background(), sess)
	require.NoError(t, err)

	byID := map[string]platform.Campaign{}
	for _, c := range camps {
		byID[c.ID] = c
	}
	require.Contains(t, byID, "campA")
	require.Contains(t, byID, "campB")

	a := byID["campA"]
	assert.Equal(t, "Rust", a.Game)
	assert.Equal(t, "active", a.Status)
	mins := map[string]int{}
	for _, bn := range a.Benefits {
		mins[bn.ID] = bn.RequiredMinutes
		assert.Equal(t, "campA", bn.CampaignID)
	}
	assert.Equal(t, 60, mins["dWatch"])
	assert.Equal(t, 0, mins["dSub"], "Review Focus #4: sub-gated drop is not watch-earnable")

	// Channel-seen campaigns are restricted to channels actually serving
	// them (alpha only; beta served nothing).
	assert.Equal(t, 1, b.AllowedChannelCount("campA"))
	// Inventory campaign keeps its own allow-list.
	assert.Equal(t, 1, b.AllowedChannelCount("campB"))
}

func TestCampaignDetails_TVSession(t *testing.T) {
	srv := fakeGQL(t, map[string]func(map[string]any) string{
		"DirectoryPage_Game": func(v map[string]any) string {
			if v["slug"] == "rust" {
				return dirRust
			}
			return `{"data":{"game":{"streams":{"edges":[]}}}}`
		},
		"DropsHighlightService_AvailableDrops": func(v map[string]any) string {
			if v["channelID"] == "c1" {
				return availAlpha
			}
			return `{"data":{"channel":{"viewerDropCampaigns":[]}}}`
		},
		"Inventory": func(map[string]any) string { return inventoryTV },
	})
	defer srv.Close()

	b := newForTest(srv.URL)
	sess := platform.Session{AccessToken: "tv", ClientID: ClientTV, Games: []string{"Rust"}}

	benefits, err := b.CampaignDetails(context.Background(), sess, "campA")
	require.NoError(t, err)
	require.NotEmpty(t, benefits)
	for _, bn := range benefits {
		assert.Equal(t, "campA", bn.CampaignID)
	}

	benefits, err = b.CampaignDetails(context.Background(), sess, "nonexistent")
	require.NoError(t, err)
	assert.Nil(t, benefits)
}

func TestListActiveCampaigns_LegacySessionUsesDashboard(t *testing.T) {
	called := map[string]bool{}
	srv := fakeGQL(t, map[string]func(map[string]any) string{
		"ViewerDropsDashboard": func(map[string]any) string {
			called["dash"] = true
			return `{"data":{"currentUser":{"dropCampaigns":[]}}}`
		},
		"DirectoryPage_Game": func(map[string]any) string { called["dir"] = true; return dirRust },
	})
	defer srv.Close()
	b := newForTest(srv.URL)
	_, err := b.ListActiveCampaigns(context.Background(), platform.Session{AccessToken: "a", Games: []string{"Rust"}})
	require.NoError(t, err)
	assert.True(t, called["dash"])
	assert.False(t, called["dir"], "Android sessions must not change discovery path")
}
