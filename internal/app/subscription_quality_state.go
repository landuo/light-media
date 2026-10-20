package app

import (
	"context"
	"encoding/json"
	"light-media/internal/subscriptionquality"
)

func qualityCopyNote(values []int) []int {
	if values == nil {
		return nil
	}
	return append([]int{}, values...)
}
func subscriptionQualityState(s Subscription) subscriptionquality.State {
	kind := subscriptionquality.Movie
	var season *int
	if s.Media.MediaType == "tv" {
		kind = subscriptionquality.TV
		n := s.Season
		season = &n
	}
	return subscriptionquality.State{Type: kind, Season: season, StartEpisode: s.StartEpisode, TotalEpisode: s.TotalEpisodes, BestVersion: s.BestVersion, BestVersionFull: s.BestVersionFull, CurrentPriority: s.CurrentPriority, EpisodePriority: s.EpisodePriority, Note: s.SourceNote, LackEpisode: s.LackEpisode, State: s.SourceState}
}
func setSubscriptionQualityState(s *Subscription, state subscriptionquality.State) {
	s.BestVersion, s.BestVersionFull = state.BestVersion, state.BestVersionFull
	s.CurrentPriority, s.EpisodePriority = state.CurrentPriority, state.EpisodePriority
	s.SourceNote, s.LackEpisode = qualityCopyNote(state.Note), state.LackEpisode
	s.TotalEpisodes = state.TotalEpisode
}
func applySubscriptionQualityState(s *Subscription, old Subscription, in SubscriptionRequest) {
	s.SourceContextSHA, s.SourceState = old.SourceContextSHA, old.SourceState
	s.SourceNote, s.SourceSearchIMDb = qualityCopyNote(old.SourceNote), old.SourceSearchIMDb
	state := subscriptionQualityState(old)
	if old.ID == "" {
		state = subscriptionQualityState(*s)
	}
	if in.BestVersion != nil {
		state.BestVersion = *in.BestVersion
	}
	if in.BestVersionFull != nil {
		state.BestVersionFull = *in.BestVersionFull
	}
	if in.SearchIMDbID != nil {
		s.SourceSearchIMDb = *in.SearchIMDbID
	}
	state.Type, state.Season, state.StartEpisode = subscriptionQualityState(*s).Type, subscriptionQualityState(*s).Season, s.StartEpisode
	if old.ID != "" && old.TotalEpisodes != s.TotalEpisodes {
		state = state.ChangeTotal(s.TotalEpisodes)
	} else {
		state.TotalEpisode = s.TotalEpisodes
	}
	setSubscriptionQualityState(s, state)
	s.ManualTotalEpisode = old.ManualTotalEpisode
	if in.ManualTotalEpisode != nil {
		s.ManualTotalEpisode = *in.ManualTotalEpisode
	} else if old.ID == "" || in.TotalEpisodes != old.TotalEpisodes {
		s.ManualTotalEpisode = in.TotalEpisodes > 0
	}
	if s.SourceState == "" {
		s.SourceState = "N"
	}
	if s.State == "paused" {
		s.SourceState = "S"
	} else if s.SourceState == "S" && s.State == "active" {
		s.SourceState = "R"
	}
}

// Origin remains an immutable creation fact. Runtime quality reads its original
// username without changing its ciphertext, hash, original note or defaults.
func (a *App) subscriptionQualitySourceState(ctx context.Context, s Subscription) (subscriptionquality.State, error) {
	state := subscriptionQualityState(s)
	if s.SourceContextSHA == "" {
		return state, nil
	}
	var sealed []byte
	if e := a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='source_subscription_origin' AND id=?`, s.ID).Scan(&sealed); e != nil {
		return state, e
	}
	raw, e := a.pluginStore.unseal("source_subscription_origin", "system", s.ID, sealed)
	if e != nil || tokenHash(string(raw)) != s.SourceContextSHA {
		return state, bad("SUBSCRIPTION_SOURCE_CHANGED", "原订阅来源身份无法核对")
	}
	var origin struct {
		Version  int
		Username json.RawMessage
	}
	if json.Unmarshal(raw, &origin) != nil || origin.Version != 1 {
		return state, bad("SUBSCRIPTION_SOURCE_CHANGED", "原订阅来源身份无法核对")
	}
	state.Username = append(json.RawMessage(nil), origin.Username...)
	return state, nil
}
