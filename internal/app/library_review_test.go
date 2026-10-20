package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/catalog"
)

func libraryReviewScan(t *testing.T, a *App) {
	t.Helper()
	job := importClaim(t, a, "library_scan", "root")
	if err := a.scanLibrary(context.Background(), job, "root"); err != nil {
		t.Fatal(err)
	}
	_, _ = a.Store.DB.Exec(`UPDATE jobs SET state='succeeded' WHERE id=?`, job.ID)
}
func libraryOnlyCandidate(t *testing.T, a *App) LibraryCandidate {
	t.Helper()
	page, e := a.libraryCandidates(context.Background(), "root", 50, "")
	if e != nil || len(page.Items) != 1 {
		t.Fatal("expected candidate", page, e)
	}
	return page.Items[0]
}
func TestLibraryReviewIndexesUnknownMovieInPlaceAndPreventsDownload(t *testing.T) {
	a, _, target := importTestApp(t)
	identity := importFixture(t, a, "movie")
	path := filepath.Join(target, "An.Unrecognized.Video.mkv")
	subtitle := filepath.Join(target, "An.Unrecognized.Video.srt")
	importWrite(t, path, []byte("existing user video"))
	importWrite(t, subtitle, []byte("existing user subtitle"))
	before, e := snapshotPath(path)
	if e != nil {
		t.Fatal(e)
	}
	libraryReviewScan(t, a)
	candidate := libraryOnlyCandidate(t, a)
	if candidate.Path != path || candidate.ReasonCode != "LIBRARY_NFO_MISSING" || candidate.Snapshot != before {
		t.Fatal("observation lost file proof", candidate)
	}
	req := LibraryRegisterRequest{CandidateID: candidate.ID, MediaType: "movie", TMDBID: identity.ID, Confirm: true}
	asset, e := a.registerLibraryAsset(context.Background(), req)
	if e != nil {
		t.Fatal(e)
	}
	if asset.Path != path || asset.SourcePath != "" || asset.State != "external" || asset.IdentitySource != "manual" || asset.IdentityConfirmedAt == "" {
		t.Fatal("registration claimed file ownership", asset)
	}
	after, _ := snapshotPath(path)
	if after != before {
		t.Fatal("registration modified video")
	}
	raw, _ := os.ReadFile(subtitle)
	if string(raw) != "existing user subtitle" {
		t.Fatal("registration changed subtitle")
	}
	entries, e := os.ReadDir(target)
	if e != nil || len(entries) != 2 {
		t.Fatal("registration created sidecars or moved file", len(entries), e)
	}
	var count int
	_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='managed_file'`).Scan(&count)
	if count != 0 {
		t.Fatal("external registration granted delete rights")
	}
	again, e := a.registerLibraryAsset(context.Background(), req)
	if e != nil || again.ID != asset.ID {
		t.Fatal("retry duplicated registered asset", again, e)
	}
	page, e := a.libraryCandidates(context.Background(), "root", 50, "")
	if e != nil || len(page.Items) != 0 {
		t.Fatal("registered candidate still pending", page, e)
	}
	subscription := Subscription{ID: "sub_existing", Media: identity, RootID: "root", State: "active", Revision: 1}
	if e = a.Store.Put(context.Background(), "subscription", subscription.ID, subscription); e != nil {
		t.Fatal(e)
	}
	coverage, e := a.subscriptionCoverage(context.Background(), subscription, nil)
	if e != nil || len(coverage.Missing) != 0 || len(coverage.Library) != 1 {
		t.Fatal("manual index did not satisfy subscription", coverage, e)
	}
	job := importClaim(t, a, "subscription", subscription.ID)
	if e = a.checkSubscription(context.Background(), job, subscription.ID, 1); e != nil {
		t.Fatal(e)
	}
	if e = a.Store.Get(context.Background(), "subscription", subscription.ID, &subscription); e != nil || subscription.State != "active" {
		t.Fatal("external inventory bypassed original completion workflow", subscription.State, e)
	}
	accepted, e := a.completionAcceptedClosure(context.Background(), a.Store.DB, subscription)
	if e != nil {
		t.Fatal(e)
	}
	basis, complete, e := a.ordinaryCompletionBasis(context.Background(), a.Store.DB, subscription, accepted)
	if e != nil || !complete || len(basis.Inventory) != 1 || basis.Inventory[0].Registration == nil || basis.Inventory[0].Video != nil {
		t.Fatal("real external identity did not give read-only completion evidence", basis, complete, e)
	}
	_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='intent'`).Scan(&count)
	if count != 0 {
		t.Fatal("existing film was downloaded again")
	}
}
func TestLibraryReviewConflictingNFORemainsUserConfirmedAfterScan(t *testing.T) {
	a, _, target := importTestApp(t)
	identity := importFixture(t, a, "movie")
	path := filepath.Join(target, "Conflict.mkv")
	nfoPath := filepath.Join(target, "Conflict.nfo")
	importWrite(t, path, []byte("external video"))
	original := []byte(`<movie><title>Contradiction</title><tmdbid>999</tmdbid><uniqueid type="tmdb">888</uniqueid></movie>`)
	importWrite(t, nfoPath, original)
	libraryReviewScan(t, a)
	candidate := libraryOnlyCandidate(t, a)
	if candidate.ReasonCode != "LIBRARY_NFO_INVALID" {
		t.Fatal(candidate)
	}
	asset, e := a.registerLibraryAsset(context.Background(), LibraryRegisterRequest{CandidateID: candidate.ID, MediaType: "movie", TMDBID: identity.ID, Confirm: true})
	if e != nil {
		t.Fatal(e)
	}
	libraryReviewScan(t, a)
	var latest Asset
	if e = a.Store.Get(context.Background(), "asset", asset.ID, &latest); e != nil || latest.Media.ID != identity.ID || latest.IdentitySource != "manual" {
		t.Fatal("scan overwrote manual identity", latest, e)
	}
	raw, _ := os.ReadFile(nfoPath)
	if string(raw) != string(original) {
		t.Fatal("manual registration changed conflicting NFO")
	}
	importWrite(t, nfoPath, []byte(`<movie><title>Now a different title</title><tmdbid>456</tmdbid></movie>`))
	libraryReviewScan(t, a)
	if e = a.Store.Get(context.Background(), "asset", asset.ID, &latest); e != nil || latest.Media.ID != identity.ID {
		t.Fatal("valid replacement NFO overrode manual identity", latest, e)
	}
	importWrite(t, path, []byte("replacement user video content"))
	libraryReviewScan(t, a)
	if e = a.Store.Get(context.Background(), "asset", asset.ID, &latest); e != nil || latest.State != "changed" || latest.Media.ID != identity.ID {
		t.Fatal("replacement video silently accepted", latest, e)
	}
	var slots int
	_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM asset_slots WHERE asset_id=?`, asset.ID).Scan(&slots)
	if slots != 1 {
		t.Fatal("changed video automatically reacquired")
	}
}
func TestLibraryReviewRejectsOldObservationAfterRescanAndRootChange(t *testing.T) {
	a, _, target := importTestApp(t)
	importFixture(t, a, "movie")
	path := filepath.Join(target, "Unknown.mkv")
	importWrite(t, path, []byte("first video"))
	libraryReviewScan(t, a)
	old := libraryOnlyCandidate(t, a)
	importWrite(t, path, []byte("different video"))
	req := LibraryRegisterRequest{CandidateID: old.ID, MediaType: "movie", TMDBID: 123, Confirm: true}
	if _, e := a.registerLibraryAsset(context.Background(), req); e == nil {
		t.Fatal("changed video accepted without rescan")
	}
	libraryReviewScan(t, a)
	current := libraryOnlyCandidate(t, a)
	if current.ID == old.ID {
		t.Fatal("new observation reused stale confirmation ID")
	}
	if _, e := a.registerLibraryAsset(context.Background(), req); e == nil {
		t.Fatal("old browser confirmation accepted new observation")
	}
	a.cfgMu.Lock()
	a.config.Roots[0].Path = filepath.Dir(target)
	a.cfgMu.Unlock()
	req.CandidateID = current.ID
	if _, e := a.registerLibraryAsset(context.Background(), req); e == nil {
		t.Fatal("changed root accepted")
	}
}
func TestLibraryReviewTVRequiresExplicitOfficialSingleEpisode(t *testing.T) {
	a, _, target := importTestApp(t)
	identity := importFixture(t, a, "tv")
	path := filepath.Join(target, "UnknownEpisode.mkv")
	importWrite(t, path, []byte("episode video"))
	libraryReviewScan(t, a)
	candidate := libraryOnlyCandidate(t, a)
	importCache(t, a, "season:123:2", catalog.Season{ID: 1202, SeriesID: 123, SeasonNumber: 2, Episodes: []catalog.Episode{{ID: 2003, SeasonNumber: 2, EpisodeNumber: 3, Name: "Known episode"}}})
	base := LibraryRegisterRequest{CandidateID: candidate.ID, MediaType: "tv", TMDBID: identity.ID, Season: 2, Confirm: true}
	for _, episodes := range [][]int{nil, {0}, {4}, {3, 4}} {
		in := base
		in.Episodes = episodes
		if _, e := a.registerLibraryAsset(context.Background(), in); e == nil {
			t.Fatal("ambiguous or unknown episode accepted", episodes)
		}
	}
	base.Episodes = []int{3}
	asset, e := a.registerLibraryAsset(context.Background(), base)
	if e != nil || asset.Season != 2 || len(asset.Episodes) != 1 || asset.Episodes[0] != 3 {
		t.Fatal(asset, e)
	}
	coverage, e := a.subscriptionCoverage(context.Background(), Subscription{Media: identity, Season: 2, Episodes: []int{3}}, []int{3})
	if e != nil || len(coverage.Missing) != 0 || len(coverage.Library) != 1 {
		t.Fatal("episode not occupied", coverage, e)
	}
	var proof libraryIdentity
	if e = a.Store.Get(context.Background(), "asset_identity", asset.ID, &proof); e != nil || proof.Source != "manual" || proof.TMDBID != 123 || proof.Episodes[0] != 3 {
		t.Fatal("missing confirmation provenance", proof, e)
	}
}
func TestLibraryReviewPaginationMissingObservationAndSymlink(t *testing.T) {
	a, _, target := importTestApp(t)
	importFixture(t, a, "movie")
	for _, name := range []string{"a.mkv", "b.mkv", "c.mkv"} {
		importWrite(t, filepath.Join(target, name), []byte(name))
	}
	outside := filepath.Join(filepath.Dir(target), "outside.mkv")
	importWrite(t, outside, []byte("outside"))
	if e := os.Symlink(outside, filepath.Join(target, "link.mkv")); e != nil {
		t.Fatal(e)
	}
	libraryReviewScan(t, a)
	first, e := a.libraryCandidates(context.Background(), "root", 2, "")
	if e != nil || len(first.Items) != 2 || first.Complete || first.NextCursor == "" {
		t.Fatal(first, e)
	}
	next, e := a.libraryCandidates(context.Background(), "root", 2, first.NextCursor)
	if e != nil || len(next.Items) != 1 || !next.Complete {
		t.Fatal(next, e)
	}
	if first.Items[1].ID >= next.Items[0].ID {
		t.Fatal("pagination duplicated candidate")
	}
	if _, e = a.libraryCandidates(context.Background(), "root", 2, first.NextCursor+"tamper"); e == nil {
		t.Fatal("unsigned cursor accepted")
	}
	if e = os.Remove(next.Items[0].Path); e != nil {
		t.Fatal(e)
	}
	libraryReviewScan(t, a)
	page, e := a.libraryCandidates(context.Background(), "root", 200, "")
	if e != nil || len(page.Items) != 2 {
		t.Fatal("gone observation still pending", page, e)
	}
}
func TestLibraryReviewHTTPRequiresConfirmation(t *testing.T) {
	a, _, target := importTestApp(t)
	importFixture(t, a, "movie")
	importWrite(t, filepath.Join(target, "Unknown.mkv"), []byte("existing movie"))
	libraryReviewScan(t, a)
	cookie := setupTestAdmin(t, a)
	h := a.Handler()
	res := callTest(t, h, "GET", "/api/assets/candidates?root_id=root&limit=1", "", cookie, "")
	if res.Code != 200 {
		t.Fatal("candidate API not wired", res.Code, res.Body)
	}
	var envelope struct {
		Data LibraryCandidatePage `json:"data"`
	}
	if e := json.Unmarshal(res.Body.Bytes(), &envelope); e != nil || len(envelope.Data.Items) != 1 {
		t.Fatal(res.Body, e)
	}
	in := LibraryRegisterRequest{CandidateID: envelope.Data.Items[0].ID, MediaType: "movie", TMDBID: 123}
	body, _ := json.Marshal(in)
	if res = callTest(t, h, "POST", "/api/assets/register", string(body), cookie, ""); res.Code != 400 {
		t.Fatal("implicit confirmation accepted", res.Code, res.Body)
	}
	in.Confirm = true
	body, _ = json.Marshal(in)
	res = callTest(t, h, "POST", "/api/assets/register", string(body), cookie, "")
	if res.Code != 200 {
		t.Fatal("registration API not usable", res.Code, res.Body)
	}
	if res = callTest(t, h, "GET", "/api/assets/candidates", "", nil, ""); res.Code != 401 {
		t.Fatal("private paths exposed without authentication", res.Code)
	}
}
