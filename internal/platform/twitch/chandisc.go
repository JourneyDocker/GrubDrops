package twitch

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/aalejandrofer/grubdrops/internal/gameslug"
	"github.com/aalejandrofer/grubdrops/internal/platform"
)

// maxChannelsPerGame bounds the AvailableDrops fan-out per whitelisted
// game. The directory is sorted by viewers; campaigns a game runs show
// up on its top channels, and in-progress ones come from Inventory.
const maxChannelsPerGame = 10

// tvBenefitEdge decodes one benefitEdges[] element, shared by
// availableDropsFull (tvDrop) and inventoryData's TimeBasedDrops so a
// benefit edge from either payload can be copied into a tvDrop without
// a Go anonymous-struct type mismatch.
type tvBenefitEdge struct {
	Benefit struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		ImageAssetURL string `json:"imageAssetURL"`
	} `json:"benefit"`
}

// availableDropsFull decodes the full AvailableDrops payload. Unlike the
// dashboard/details queries, Twitch serves it to TV-client tokens.
type availableDropsFull struct {
	Channel struct {
		ViewerDropCampaigns []struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			EndAt string `json:"endAt"`
			Game  struct {
				Name string `json:"name"`
			} `json:"game"`
			TimeBasedDrops []tvDrop `json:"timeBasedDrops"`
		} `json:"viewerDropCampaigns"`
	} `json:"channel"`
}

type tvDrop struct {
	ID                     string          `json:"id"`
	Name                   string          `json:"name"`
	RequiredMinutesWatched int             `json:"requiredMinutesWatched"`
	RequiredSubs           int             `json:"requiredSubs"`
	BenefitEdges           []tvBenefitEdge `json:"benefitEdges"`
}

// toBenefits flattens drops the same way fetchDetails does, including
// the #47 rule: sub-gated drops report 0 minutes (not watch-earnable).
func toBenefits(campaignID string, drops []tvDrop) []platform.DropBenefit {
	var out []platform.DropBenefit
	for _, td := range drops {
		req := td.RequiredMinutesWatched
		if td.RequiredSubs > 0 {
			req = 0
		}
		for _, be := range td.BenefitEdges {
			out = append(out, platform.DropBenefit{
				ID: td.ID, CampaignID: campaignID, Name: be.Benefit.Name,
				RequiredMinutes: req, ImageURL: be.Benefit.ImageAssetURL, RewardID: be.Benefit.ID,
			})
		}
	}
	return out
}

// listByChannels discovers campaigns for sessions that can't see the
// drops dashboard (TV client). Directory(DROPS_ENABLED) per whitelisted
// game → AvailableDrops per top channel, merged with Inventory's
// in-progress campaigns (which AvailableDrops may omit, and which carry
// their own allow-lists). Returns campaigns + campaignID→allowed logins.
func (d *discovery) listByChannels(ctx context.Context, sess platform.Session, ch *channels) ([]platform.Campaign, map[string][]string, error) {
	camps := map[string]*platform.Campaign{}
	allowed := map[string][]string{}
	order := []string{}
	add := func(c platform.Campaign) *platform.Campaign {
		if ex, ok := camps[c.ID]; ok {
			return ex
		}
		cc := c
		camps[c.ID] = &cc
		order = append(order, c.ID)
		return &cc
	}

	// sess.Games may carry a game under more than one token — the discovery
	// scraper's whitelist union emits both a game's lowercased display name
	// and its lowercased slug (e.g. "grand theft auto v" AND
	// "grand-theft-auto-v"), and gameslug.Slug maps both to the same value.
	// Dedupe by slug (skipping empty ones) so a multi-word game doesn't
	// double the DirectoryPage_Game + AvailableDrops fan-out every tick.
	seenSlugs := make(map[string]struct{}, len(sess.Games))
	for _, game := range sess.Games {
		slug := gameslug.Slug(game)
		if slug == "" {
			continue
		}
		if _, dup := seenSlugs[slug]; dup {
			continue
		}
		seenSlugs[slug] = struct{}{}

		streams, err := ch.listForGameDirectory(ctx, sess, slug)
		if err != nil {
			slog.Warn("tv discovery: directory failed", "game", game, "err", err)
			continue // one bad game must not sink the rest
		}
		if len(streams) > maxChannelsPerGame {
			streams = streams[:maxChannelsPerGame]
		}
		for _, s := range streams {
			var resp availableDropsFull
			if err := d.c.gql(ctx, sess.AccessToken, OpAvailableDrops, map[string]any{"channelID": s.ChannelID}, &resp); err != nil {
				slog.Warn("tv discovery: available drops failed", "channel", s.Channel, "err", err)
				continue
			}
			for _, vc := range resp.Channel.ViewerDropCampaigns {
				add(platform.Campaign{
					ID: vc.ID, Platform: "twitch", Game: vc.Game.Name, Name: vc.Name,
					EndsAt: parseISO(vc.EndAt), Status: "active", Kind: "drop",
					// Link state is unknowable without DropCampaignDetails;
					// optimistic like scrape-sourced campaigns.
					AccountLinked: true, AccountLinkChecked: false,
					Benefits: toBenefits(vc.ID, vc.TimeBasedDrops),
				})
				allowed[vc.ID] = appendUnique(allowed[vc.ID], s.Channel)
			}
		}
	}

	var inv inventoryData
	if err := d.c.gql(ctx, sess.AccessToken, OpInventory, nil, &inv); err != nil {
		return nil, nil, fmt.Errorf("tv discovery inventory: %w", err)
	}
	for _, ic := range inv.CurrentUser.Inventory.DropCampaignsInProgress {
		drops := make([]tvDrop, 0, len(ic.TimeBasedDrops))
		for _, td := range ic.TimeBasedDrops {
			drops = append(drops, tvDrop{
				ID: td.ID, Name: td.Name,
				RequiredMinutesWatched: td.RequiredMinutesWatched,
				RequiredSubs:           td.RequiredSubs,
				BenefitEdges:           td.BenefitEdges,
			})
		}
		add(platform.Campaign{
			ID: ic.ID, Platform: "twitch", Game: ic.Game.Name, Name: ic.Name,
			EndsAt: parseISO(ic.EndAt), Status: "active", Kind: "drop",
			AccountLinked: true, AccountLinkChecked: false,
			Benefits: toBenefits(ic.ID, drops),
		})
		if len(ic.Allow.Channels) > 0 {
			var logins []string
			for _, c := range ic.Allow.Channels {
				logins = appendUnique(logins, c.Name)
			}
			allowed[ic.ID] = logins // Twitch's own allow-list wins
		}
	}

	out := make([]platform.Campaign, 0, len(order))
	for _, id := range order {
		out = append(out, *camps[id])
	}
	return out, allowed, nil
}

func appendUnique(xs []string, x string) []string {
	for _, e := range xs {
		if e == x {
			return xs
		}
	}
	return append(xs, x)
}
