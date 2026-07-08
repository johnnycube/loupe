package main

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnnycube/loupe/internal/repo"
)

func stringReader(s string) *strings.Reader { return strings.NewReader(s) }

var bg = context.Background()

/*
These tests exercise the pure logic and the HTTP handlers directly, with no
network access and no dependency on gallery-dl. The data layer is the package
global `store`; each test calls newStore() to swap in a fresh in-memory SQLite
database, so the real data store is never touched.
*/

func TestMain(m *testing.M) {
	// handleSources would spawn `go pollOne`, whose goroutine then races with the
	// shared store as later tests mutate it. Disable the background poll, and
	// point gallery-dl at a no-op for any direct poll call.
	pollAsync = func(string) {}
	pollAsyncFull = func(string) {}
	cfg.GDL = "true"
	newStore()
	code := m.Run()
	if store != nil {
		_ = store.Close()
	}
	os.Exit(code)
}

// newStore swaps in a fresh, empty in-memory SQLite-backed Repo. Each :memory:
// DSN is its own database held open by the single pooled connection, so tests
// start from a clean slate with no shared state.
func newStore() {
	if store != nil {
		_ = store.Close()
	}
	r, err := repo.Open("sqlite", ":memory:")
	if err != nil {
		panic(err)
	}
	store = r
}

func addSource(id, name, url string) {
	if err := store.InsertSource(bg, &Source{ID: id, Name: name, URL: url, AddedAt: now()}); err != nil {
		panic(err)
	}
}

func addItem(sourceID, key, status string, gone bool) *Item {
	id := makeID(sourceID, key)
	it := &Item{ID: id, SourceID: sourceID, ImageKey: key, Label: "x",
		URL: "http://img/" + key + ".jpg", Status: status, Gone: gone, AddedAt: now()}
	if _, err := store.InsertItemIfNew(bg, it); err != nil {
		panic(err)
	}
	return it
}

/* ----------------------------------------------------------- test helpers */

func getItem(id string) *Item {
	it, ok, err := store.GetItem(bg, id)
	if err != nil || !ok {
		return nil
	}
	return it
}

func getSource(id string) *Source {
	s, ok, err := store.GetSource(bg, id)
	if err != nil || !ok {
		return nil
	}
	return s
}

func firstSource() *Source {
	srcs, _ := store.ListSources(bg)
	if len(srcs) == 0 {
		return nil
	}
	return srcs[0]
}

func sourceCount() int {
	srcs, _ := store.ListSources(bg)
	return len(srcs)
}

func itemCount() int {
	rows, _ := store.CountByStatusGone(bg)
	n := 0
	for _, row := range rows {
		n += row.N
	}
	return n
}

/* ----------------------------------------------------------- pure helpers */

func TestParseDump(t *testing.T) {
	// gallery-dl -j emits an array of [type, url, metadata] messages.
	// type 3 = file Url (strict); 2 = Directory, 6 = Queue (both ignored).
	in := `[
		[2, "http://x/dir"],
		[3, "http://x/1.jpg", {"md5":"aaa","title":"One","sample_url":"http://x/s1.jpg"}],
		[3, "http://x/2.jpg", {"id": 42}],
		[3, "ftp://x/skip.jpg", {"id": 99}],
		[6, "http://x/queued"]
	]`
	got := parseDump(in)
	if len(got) != 2 {
		t.Fatalf("want 2 strict items, got %d: %+v", len(got), got)
	}
	if got[0].imageKey != "aaa" || got[0].sample != "http://x/s1.jpg" || got[0].title != "One" {
		t.Errorf("item0 wrong: %+v", got[0])
	}
	// numeric id is stringified; sample falls back to the file url.
	if got[1].imageKey != "42" || got[1].sample != "http://x/2.jpg" {
		t.Errorf("item1 wrong: %+v", got[1])
	}
}

func TestParseDumpRedditPreview(t *testing.T) {
	// Reddit posts carry sized previews instead of a sample_url. Single-image
	// posts use preview.images[0]; galleries use media_metadata keyed by the
	// i.redd.it image id. Both must be preferred over the tiny `thumbnail`, and
	// reddit's &amp; entities must be unescaped.
	in := `[
		[3, "https://i.redd.it/single1.jpg", {
			"id": "p1", "thumbnail": "https://b.thumbs.redditmedia.com/x_140.jpg",
			"preview": {"images": [{
				"resolutions": [
					{"url": "https://preview.redd.it/single1.jpg?width=108&amp;s=a", "width": 108},
					{"url": "https://preview.redd.it/single1.jpg?width=640&amp;s=b", "width": 640}
				],
				"source": {"url": "https://preview.redd.it/single1.jpg?width=1920&amp;s=c", "width": 1920}
			}]}
		}],
		[3, "https://i.redd.it/galA.jpg", {
			"id": "p2", "thumbnail": "https://b.thumbs.redditmedia.com/y_140.jpg",
			"media_metadata": {
				"galA": {"p": [{"u": "https://preview.redd.it/galA.jpg?width=320&amp;s=d", "x": 320}], "s": {"u": "https://preview.redd.it/galA.jpg?width=2000&amp;s=e", "x": 2000}},
				"galB": {"p": [{"u": "https://preview.redd.it/galB.jpg?width=320&amp;s=f", "x": 320}]}
			}
		}]
	]`
	got := parseDump(in)
	if len(got) != 2 {
		t.Fatalf("want 2 items, got %d: %+v", len(got), got)
	}
	// Single post: widest resolution wins, entities unescaped.
	if got[0].sample != "https://preview.redd.it/single1.jpg?width=640&s=b" {
		t.Errorf("single preview wrong: %q", got[0].sample)
	}
	// Gallery member galA: its own media_metadata entry, not galB or thumbnail.
	if got[1].sample != "https://preview.redd.it/galA.jpg?width=320&s=d" {
		t.Errorf("gallery preview wrong: %q", got[1].sample)
	}
}

func TestParseDumpLooseFallback(t *testing.T) {
	// No type-3 messages → loose types (anything but 2 and 6) are used.
	in := `[[1, "http://x/a.jpg", {"id":"a"}]]`
	got := parseDump(in)
	if len(got) != 1 || got[0].imageKey != "a" {
		t.Fatalf("expected loose fallback to 1 item, got %+v", got)
	}
}

func TestParseDumpGarbage(t *testing.T) {
	if got := parseDump("not json"); got != nil {
		t.Errorf("garbage should parse to nil, got %+v", got)
	}
}

func TestParseError(t *testing.T) {
	// gallery-dl reports failures as a type -1 message and still exits 0.
	authErr := `[[-1, {"error":"AuthRequired","message":"'api-key' & 'user-id' needed"}]]`
	if got := parseError(authErr); got != "AuthRequired: 'api-key' & 'user-id' needed" {
		t.Errorf("auth error: got %q", got)
	}
	// message-only and error-only shapes.
	if got := parseError(`[[-1, {"message":"rate limited"}]]`); got != "rate limited" {
		t.Errorf("message-only: got %q", got)
	}
	// A normal dump with real items has no error.
	ok := `[[3, "http://x/1.jpg", {"id":"a"}]]`
	if got := parseError(ok); got != "" {
		t.Errorf("healthy dump should have no error, got %q", got)
	}
	if got := parseError("not json"); got != "" {
		t.Errorf("garbage should yield no error, got %q", got)
	}
}

func TestMakeIDIsPerSource(t *testing.T) {
	// Same image key under two sources must yield distinct item IDs —
	// that's the "decisions are per source" invariant.
	if makeID("srcA", "img1") == makeID("srcB", "img1") {
		t.Error("makeID collided across sources")
	}
	if makeID("srcA", "img1") != makeID("srcA", "img1") {
		t.Error("makeID not stable")
	}
}

func TestBumpCounts(t *testing.T) {
	c := newCounts()
	bump(c, &Item{Status: "new"})              // new
	bump(c, &Item{Status: "good"})             // good
	bump(c, &Item{Status: "bad"})              // bad
	bump(c, &Item{Status: "good", Gone: true}) // gone + good (kept keeper)
	bump(c, &Item{Status: "new", Gone: true})  // gone + staleNew
	want := map[string]int{"new": 1, "good": 2, "bad": 1, "gone": 2, "staleNew": 1}
	for k, v := range want {
		if c[k] != v {
			t.Errorf("count[%s]=%d want %d (full: %+v)", k, c[k], v, c)
		}
	}
}

/* ----------------------------------------------------------- handlers */

func decode(t *testing.T, rr *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rr.Body.Bytes(), v); err != nil {
		t.Fatalf("decode: %v — body=%s", err, rr.Body.String())
	}
}

func TestNextScopeFiltering(t *testing.T) {
	newStore()
	addSource("A", "Alpha", "http://a")
	addSource("B", "Beta", "http://b")
	if err := store.InsertCollection(bg, &Collection{ID: "C", Name: "Col", SourceIDs: []string{"A"}}); err != nil {
		t.Fatal(err)
	}
	addItem("A", "1", "new", false)
	addItem("B", "2", "new", false)
	addItem("A", "3", "good", false) // decided → never in /next
	addItem("A", "4", "new", true)   // stale → only in scope=stale

	cases := map[string]int{
		"all":          2, // A1 + B2
		"source:A":     1, // A1
		"source:B":     1, // B2
		"collection:C": 1, // A1
		"stale":        1, // A4
	}
	for scope, want := range cases {
		rr := httptest.NewRecorder()
		handleNext(rr, httptest.NewRequest("GET", "/api/next?scope="+scope, nil))
		var out []map[string]any
		decode(t, rr, &out)
		if len(out) != want {
			t.Errorf("scope=%s: got %d items, want %d", scope, len(out), want)
		}
	}
}

func TestInterleaveBySource(t *testing.T) {
	// Source A has 3 (newer) items, B has 1. Output must mix B in, not dump all
	// of A first. Within a source, newest-first; freshest source leads each round.
	a1 := &Item{ID: "a1", SourceID: "A", AddedAt: 100}
	a2 := &Item{ID: "a2", SourceID: "A", AddedAt: 99}
	a3 := &Item{ID: "a3", SourceID: "A", AddedAt: 98}
	b1 := &Item{ID: "b1", SourceID: "B", AddedAt: 50}
	got := interleaveBySource([]*Item{a3, a1, b1, a2})
	want := []string{"a1", "b1", "a2", "a3"} // round0: a1,b1 · round1: a2 · round2: a3
	if len(got) != len(want) {
		t.Fatalf("len %d want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Errorf("pos %d = %s, want %s (full: %v)", i, got[i].ID, id, ids(got))
		}
	}
}

func ids(items []*Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.ID
	}
	return out
}

func TestNextDeepLinkPinsFirst(t *testing.T) {
	newStore()
	addSource("A", "Alpha", "http://a")
	addItem("A", "1", "new", false)
	target := addItem("A", "2", "new", false)
	addItem("A", "3", "new", false)
	rr := httptest.NewRecorder()
	handleNext(rr, httptest.NewRequest("GET", "/api/next?scope=all&first="+target.ID, nil))
	var out []map[string]any
	decode(t, rr, &out)
	if len(out) == 0 || out[0]["id"] != target.ID {
		t.Fatalf("deep-linked item should be first, got %v", out)
	}
	n := 0
	for _, x := range out {
		if x["id"] == target.ID {
			n++
		}
	}
	if n != 1 {
		t.Errorf("pinned item appears %d times, want exactly 1", n)
	}
}

func TestNextDeepLinkIgnoresUnreviewable(t *testing.T) {
	newStore()
	addSource("A", "Alpha", "http://a")
	addItem("A", "1", "new", false)
	decided := addItem("A", "2", "good", false) // already decided → not reviewable
	rr := httptest.NewRecorder()
	handleNext(rr, httptest.NewRequest("GET", "/api/next?scope=all&first="+decided.ID, nil))
	var out []map[string]any
	decode(t, rr, &out)
	for _, x := range out {
		if x["id"] == decided.ID {
			t.Error("a decided item must not be pinned or returned")
		}
	}
}

func TestVoteUnvoteSelectsFlow(t *testing.T) {
	newStore()
	addSource("A", "Alpha", "http://a")
	it := addItem("A", "1", "new", false)

	post := func(h http.HandlerFunc, body string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		h(rr, httptest.NewRequest("POST", "/", stringReader(body)))
		return rr
	}

	// vote good
	post(handleVote, `{"id":"`+it.ID+`","decision":"good"}`)
	if s := getItem(it.ID).Status; s != "good" {
		t.Fatalf("vote good failed: %q", s)
	}
	// it now shows up in selects
	rr := httptest.NewRecorder()
	handleSelects(rr, httptest.NewRequest("GET", "/api/selects", nil))
	var sel []map[string]any
	decode(t, rr, &sel)
	if len(sel) != 1 {
		t.Fatalf("expected 1 select, got %d", len(sel))
	}
	// unselect → bad
	post(handleUnselect, `{"id":"`+it.ID+`"}`)
	if s := getItem(it.ID).Status; s != "bad" {
		t.Errorf("unselect should set bad, got %q", s)
	}
	// unvote → back to new (returns to queue)
	post(handleUnvote, `{"id":"`+it.ID+`"}`)
	if s := getItem(it.ID).Status; s != "new" {
		t.Errorf("unvote should restore new, got %q", s)
	}
}

func TestVoteRejectsBadDecision(t *testing.T) {
	newStore()
	rr := httptest.NewRecorder()
	handleVote(rr, httptest.NewRequest("POST", "/", stringReader(`{"id":"x","decision":"maybe"}`)))
	if rr.Code != 400 {
		t.Errorf("bad decision should be 400, got %d", rr.Code)
	}
}

func TestVoteUnknownIsNotFound(t *testing.T) {
	newStore()
	rr := httptest.NewRecorder()
	handleVote(rr, httptest.NewRequest("POST", "/", stringReader(`{"id":"nope","decision":"good"}`)))
	if rr.Code != 404 {
		t.Errorf("vote on unknown id should be 404, got %d", rr.Code)
	}
}

func TestAddSourceValidation(t *testing.T) {
	newStore()
	rr := httptest.NewRecorder()
	handleSources(rr, httptest.NewRequest("POST", "/api/sources", stringReader(`{"url":"notaurl"}`)))
	if rr.Code != 400 {
		t.Errorf("non-http url should be 400, got %d", rr.Code)
	}
	if sourceCount() != 0 {
		t.Errorf("invalid source should not be stored")
	}
}

func TestExpandPath(t *testing.T) {
	home, _ := os.UserHomeDir()
	if got := expandPath("~/x/y.conf"); got != filepath.Join(home, "x/y.conf") {
		t.Errorf("~/ not expanded: %q", got)
	}
	if got := expandPath("/abs/path.conf"); got != "/abs/path.conf" {
		t.Errorf("absolute path mangled: %q", got)
	}
	if got := expandPath(""); got != "" {
		t.Errorf("empty path changed: %q", got)
	}
}

func TestAddSourceStoresConfigFile(t *testing.T) {
	newStore()
	rr := httptest.NewRecorder()
	handleSources(rr, httptest.NewRequest("POST", "/api/sources",
		stringReader(`{"url":"http://x/gallery","configFile":"~/.config/gallery-dl/booru.conf"}`)))
	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if got := firstSource().ConfigFile; got != "~/.config/gallery-dl/booru.conf" {
		t.Errorf("configFile not stored: %q", got)
	}
}

func TestPollMissingConfigFileSurfacesError(t *testing.T) {
	// A non-existent per-source config must fail loudly (and never invoke
	// gallery-dl), not silently fall back to defaults.
	newStore()
	addSource("A", "Alpha", "http://a")
	added, missing := pollSourceSnapshot("A", "http://a", "Alpha", "/no/such/loupe-config.conf", "", "")
	if added != 0 || missing {
		t.Errorf("expected no-op, got added=%d missing=%v", added, missing)
	}
	if getSource("A").LastError == "" {
		t.Error("missing config file should set LastError")
	}
}

func TestMergeExtractedDedupAndUnstale(t *testing.T) {
	newStore()
	addSource("A", "Alpha", "http://a")
	// mark one item stale to prove a re-seen item gets un-staled
	stale := addItem("A", "1", "good", true)

	items := []extracted{
		{url: "http://a/1.jpg", imageKey: "1"}, // already exists (stale) → refreshed, not added
		{url: "http://a/2.jpg", imageKey: "2"}, // new
		{url: "http://a/3.jpg", imageKey: "3"}, // new
	}
	added := mergeExtracted("A", "Alpha", items)
	if added != 2 {
		t.Errorf("expected 2 newly added, got %d", added)
	}
	if it := getItem(stale.ID); it == nil || it.Gone {
		t.Error("re-seen item should be un-staled")
	}
	if itemCount() != 3 {
		t.Errorf("expected 3 items total, got %d", itemCount())
	}
}

func TestPollFullMissingConfigSurfacesError(t *testing.T) {
	newStore()
	addSource("A", "Alpha", "http://a")
	added, missing := pollSourceFull("A", "http://a", "Alpha", "/no/such/cfg.conf", "", "")
	if added != 0 || missing {
		t.Errorf("expected no-op, got added=%d missing=%v", added, missing)
	}
	if getSource("A").LastError == "" {
		t.Error("missing config file should set LastError on full scan")
	}
}

// readStagedConfigs returns the contents of every `-c <tmpfile>` JSON body that
// buildConfigArgs staged, in order. File-path -c entries (which point at real
// on-disk configs, not temp files) are skipped.
func readStagedConfigs(t *testing.T, pre []string) []string {
	t.Helper()
	var bodies []string
	for i := 0; i+1 < len(pre); i += 2 {
		if pre[i] != "-c" {
			continue
		}
		if b, err := os.ReadFile(pre[i+1]); err == nil {
			bodies = append(bodies, string(b))
		}
	}
	return bodies
}

func TestSharedConfigResolvesAndPropagates(t *testing.T) {
	newStore()
	// A shared config referenced by a source is staged for gallery-dl, and
	// editing it changes what the next poll sees — the whole point of sharing.
	if err := store.InsertConfig(bg, &repo.Config{ID: "c1", Name: "reddit", ConfigJSON: `{"extractor":{"reddit":{"a":1}}}`, AddedAt: now()}); err != nil {
		t.Fatal(err)
	}
	pre, cleanup, msg := buildConfigArgs("", "", "c1")
	if msg != "" {
		t.Fatalf("unexpected error: %s", msg)
	}
	bodies := readStagedConfigs(t, pre)
	cleanup()
	if len(bodies) != 1 || !strings.Contains(bodies[0], `"a":1`) {
		t.Fatalf("shared config not staged: %v", bodies)
	}

	// Edit the config; a fresh build must reflect it without touching the source.
	if err := store.UpdateConfig(bg, &repo.Config{ID: "c1", Name: "reddit", ConfigJSON: `{"extractor":{"reddit":{"a":2}}}`}); err != nil {
		t.Fatal(err)
	}
	pre2, cleanup2, _ := buildConfigArgs("", "", "c1")
	bodies2 := readStagedConfigs(t, pre2)
	cleanup2()
	if len(bodies2) != 1 || !strings.Contains(bodies2[0], `"a":2`) {
		t.Fatalf("edited shared config not reflected: %v", bodies2)
	}
}

func TestSharedConfigLayersBeforeInline(t *testing.T) {
	newStore()
	// Order matters: shared config is the base, the source's own inline JSON
	// overrides it — so inline must come last in the -c list.
	if err := store.InsertConfig(bg, &repo.Config{ID: "c1", Name: "base", ConfigJSON: `{"shared":true}`, AddedAt: now()}); err != nil {
		t.Fatal(err)
	}
	pre, cleanup, msg := buildConfigArgs("", `{"inline":true}`, "c1")
	if msg != "" {
		t.Fatalf("unexpected error: %s", msg)
	}
	bodies := readStagedConfigs(t, pre)
	cleanup()
	if len(bodies) != 2 || !strings.Contains(bodies[0], "shared") || !strings.Contains(bodies[1], "inline") {
		t.Fatalf("expected [shared, inline] order, got %v", bodies)
	}
}

func TestSharedConfigMissingSurfacesError(t *testing.T) {
	newStore()
	// A reference to a deleted config must fail loudly, not poll without it.
	_, cleanup, msg := buildConfigArgs("", "", "ghost")
	cleanup()
	if msg == "" {
		t.Error("dangling shared config reference should produce an error")
	}
}

func TestValidateConfigJSON(t *testing.T) {
	if msg := validateConfigJSON(""); msg != "" {
		t.Errorf("empty should be valid, got %q", msg)
	}
	if msg := validateConfigJSON(`{"extractor":{"rule34":{"api-key":"x"}}}`); msg != "" {
		t.Errorf("valid JSON rejected: %q", msg)
	}
	if msg := validateConfigJSON(`{not json`); msg == "" {
		t.Error("invalid JSON should be rejected")
	}
}

func TestAddSourceRejectsBadConfigJSON(t *testing.T) {
	newStore()
	rr := httptest.NewRecorder()
	handleSources(rr, httptest.NewRequest("POST", "/api/sources",
		stringReader(`{"url":"http://x/g","configJson":"{ broken"}`)))
	if rr.Code != 400 {
		t.Errorf("bad config JSON should be 400, got %d", rr.Code)
	}
	if sourceCount() != 0 {
		t.Error("source with invalid config JSON should not be stored")
	}
}

func TestAddSourceStoresConfigJSON(t *testing.T) {
	newStore()
	rr := httptest.NewRecorder()
	handleSources(rr, httptest.NewRequest("POST", "/api/sources",
		stringReader(`{"url":"http://x/g","configJson":"{\"extractor\":{\"rule34\":{\"user-id\":\"42\"}}}"}`)))
	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d — %s", rr.Code, rr.Body.String())
	}
	if stored := firstSource().ConfigJSON; stored == "" || !strings.Contains(stored, "rule34") {
		t.Errorf("configJson not stored: %q", stored)
	}
}

func TestRescanUnknownSource(t *testing.T) {
	newStore()
	rr := httptest.NewRecorder()
	handleSourceRescan(rr, httptest.NewRequest("POST", "/api/sources/rescan", stringReader(`{"id":"nope"}`)))
	if rr.Code != 404 {
		t.Errorf("rescan of unknown source should be 404, got %d", rr.Code)
	}
}

func TestStats(t *testing.T) {
	newStore()
	addSource("A", "Alpha", "http://a")
	addItem("A", "1", "new", false)
	addItem("A", "2", "good", false)
	rr := httptest.NewRecorder()
	handleStats(rr, httptest.NewRequest("GET", "/api/stats", nil))
	var s struct {
		Total   int            `json:"total"`
		Counts  map[string]int `json:"counts"`
		Sources int            `json:"sources"`
	}
	decode(t, rr, &s)
	if s.Total != 2 || s.Sources != 1 || s.Counts["new"] != 1 || s.Counts["good"] != 1 {
		t.Errorf("stats wrong: %+v", s)
	}
}

func TestRunGdlTimeoutSurfaces(t *testing.T) {
	// A run killed by the timeout must produce a visible error message, not an
	// empty stderr that reads as "0 new items" (gallery-dl -j prints its dump
	// only at the end of a run, so a killed run has no usable stdout).
	oldGDL, oldTO := cfg.GDL, cfg.TimeoutSec
	defer func() { cfg.GDL, cfg.TimeoutSec = oldGDL, oldTO }()
	cfg.GDL, cfg.TimeoutSec = "sleep", 1
	_, errs, code, missing := runGdl("5")
	if missing || code == 0 {
		t.Fatalf("timed-out run should fail: code=%d missing=%v", code, missing)
	}
	if !strings.Contains(errs, "timed out") || !strings.Contains(errs, "LOUPE_GDL_TIMEOUT_SEC") {
		t.Errorf("timeout must surface an actionable message, got %q", errs)
	}
	if msg := pageError("", errs, code); !strings.Contains(msg, "timed out") {
		t.Errorf("pageError should propagate the timeout, got %q", msg)
	}
}

/* ----------------------------------------------------------- security guard */

func TestGuardHostAndOrigin(t *testing.T) {
	ok := 0
	h := guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ok++; w.WriteHeader(200) }))
	do := func(method, host, origin, ct string) int {
		r := httptest.NewRequest(method, "http://placeholder/api/vote", stringReader(`{}`))
		r.Host = host
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if ct != "" {
			r.Header.Set("Content-Type", ct)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, r)
		return rr.Code
	}

	// Localhost, IPs (v4/v6, with port) and *.localhost pass; foreign names don't.
	for _, host := range []string{"localhost:8787", "127.0.0.1:8787", "192.168.1.20:8787", "[::1]:8787", "loupe.localhost"} {
		if code := do("GET", host, "", ""); code != 200 {
			t.Errorf("host %q should pass, got %d", host, code)
		}
	}
	// DNS rebinding: the browser reaches us but sends the attacker's hostname.
	if code := do("GET", "evil.example.com", "", ""); code != 403 {
		t.Errorf("foreign host should be 403, got %d", code)
	}

	// CSRF: a cross-site POST is rejected on Content-Type (HTML forms can't send
	// JSON) and on Origin mismatch even when the type is right.
	if code := do("POST", "localhost:8787", "", "application/x-www-form-urlencoded"); code != 415 {
		t.Errorf("form-encoded POST should be 415, got %d", code)
	}
	if code := do("POST", "localhost:8787", "http://evil.example.com", "application/json"); code != 403 {
		t.Errorf("cross-origin POST should be 403, got %d", code)
	}
	// The app's own requests pass: same-origin, JSON.
	if code := do("POST", "localhost:8787", "http://localhost:8787", "application/json"); code != 200 {
		t.Errorf("same-origin JSON POST should pass, got %d", code)
	}
	// A proxy that rewrites Host (Vite dev, reverse proxies): the Origin differs
	// from Host but is itself an allowed host, so it must pass.
	if code := do("POST", "localhost:8787", "http://localhost:5173", "application/json"); code != 200 {
		t.Errorf("allowed-host origin behind a host-rewriting proxy should pass, got %d", code)
	}
	if code := do("POST", "localhost:8787", "", "application/json; charset=utf-8"); code != 200 {
		t.Errorf("originless JSON POST (curl) should pass, got %d", code)
	}
}

func TestGuardAllowedHostsEnv(t *testing.T) {
	t.Setenv("LOUPE_ALLOWED_HOSTS", "loupe.example.com, media.lan")
	h := guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	do := func(host string) int {
		r := httptest.NewRequest("GET", "http://placeholder/api/stats", nil)
		r.Host = host
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, r)
		return rr.Code
	}
	if code := do("loupe.example.com"); code != 200 {
		t.Errorf("allowlisted host should pass, got %d", code)
	}
	if code := do("MEDIA.LAN:8787"); code != 200 {
		t.Errorf("allowlist should be case-insensitive and ignore the port, got %d", code)
	}
	if code := do("other.example.com"); code != 403 {
		t.Errorf("non-listed host should be 403, got %d", code)
	}
}

func TestPostOnly(t *testing.T) {
	h := postOnly(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest("GET", "/api/vote", nil))
	if rr.Code != 405 {
		t.Errorf("GET on a mutating endpoint should be 405, got %d", rr.Code)
	}
	rr = httptest.NewRecorder()
	h(rr, httptest.NewRequest("POST", "/api/vote", stringReader(`{}`)))
	if rr.Code != 200 {
		t.Errorf("POST should pass, got %d", rr.Code)
	}
}

func TestZipEntryName(t *testing.T) {
	cases := map[string]string{
		"http://x/a/photo.jpg":           "photo.jpg",
		"http://x/photo.jpg?sig=..%5C..": "photo.jpg",
		`http://x/..\..\evil.exe`:        "_.._evil.exe", // backslashes neutralised, dots trimmed
		"http://x/...":                   "k.jpg",        // dots only → image key
	}
	for in, want := range cases {
		if got := zipEntryName(in, "k"); got != want {
			t.Errorf("zipEntryName(%q) = %q, want %q", in, got, want)
		}
	}
}

/* --------------------------------------------------- embedded static serving */

// findEmbeddedAsset returns the path (relative to the build root) of a real
// hashed asset under _app/ in the embedded build, so the test asserts against
// whatever the current frontend build produced rather than a name that churns.
func findEmbeddedAsset(t *testing.T) string {
	t.Helper()
	var found string
	fs.WalkDir(buildFS, "frontend/build/_app", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".js") {
			found = strings.TrimPrefix(p, "frontend/build/")
			return fs.SkipAll
		}
		return nil
	})
	if found == "" {
		t.Fatal("no _app/*.js asset found in embedded build")
	}
	return found
}

func TestStaticHandlerAssetVsNavigation(t *testing.T) {
	// The compiled UI is a build artifact and isn't committed, so on a fresh
	// checkout the embed holds only a placeholder. Skip rather than fail when the
	// frontend hasn't been built (`make build-frontend`); CI/Docker build it first.
	if _, err := fs.Stat(buildFS, "frontend/build/index.html"); err != nil {
		t.Skip("frontend not built — run `make build-frontend` to exercise the embedded UI")
	}
	h := staticHandler()
	do := func(p string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest("GET", p, nil))
		return rr
	}

	// A real hashed asset is served as-is, not rewritten to the HTML shell.
	// Discriminate on Content-Type, not body: SvelteKit chunks can themselves
	// contain the literal "<!doctype html>" string.
	asset := findEmbeddedAsset(t)
	if rr := do("/" + asset); rr.Code != http.StatusOK ||
		!strings.Contains(rr.Header().Get("Content-Type"), "javascript") {
		t.Errorf("real asset %q: code=%d, content-type=%q (want 200, js)",
			asset, rr.Code, rr.Header().Get("Content-Type"))
	}

	// A stale/missing hashed asset must 404 — never fall back to index.html,
	// which would serve HTML in place of JS and break the page silently.
	for _, miss := range []string{
		"/_app/immutable/chunks/THIS_HASH_IS_GONE.js",
		"/_app/immutable/entry/start.OLDHASH.js",
		"/nope.png",
	} {
		rr := do(miss)
		if rr.Code != http.StatusNotFound {
			t.Errorf("missing asset %q: code=%d, want 404 (body starts %.20q)",
				miss, rr.Code, rr.Body.String())
		}
	}

	// Navigation routes (extensionless, non-_app) fall back to the SPA shell.
	for _, nav := range []string{"/", "/gallery", "/collections/abc"} {
		rr := do(nav)
		if rr.Code != http.StatusOK ||
			!strings.Contains(rr.Header().Get("Content-Type"), "text/html") {
			t.Errorf("nav route %q: code=%d, content-type=%q (want 200, html)",
				nav, rr.Code, rr.Header().Get("Content-Type"))
		}
	}
}
