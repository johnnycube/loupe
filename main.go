package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/johnnycube/loupe/internal/repo"
)

/* ------------------------------------------------------------------ config */

type Config struct {
	Addr         string
	Port         string
	PollMin      int
	PerSource    int
	RescanMax    int
	PreloadDepth int
	GDL          string
	TimeoutSec   int
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func envInt(k string, d int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return d
}

// All configuration is read from LOUPE_-prefixed environment variables.
var cfg = Config{
	Addr:         env("LOUPE_HTTP_ADDR", ""), // bind host; empty = all interfaces
	Port:         env("LOUPE_HTTP_PORT", "8787"),
	PollMin:      envInt("LOUPE_POLL_MINUTES", 15),
	PerSource:    envInt("LOUPE_PER_SOURCE", 50), // newest-N window for the scheduled poll
	RescanMax:    envInt("LOUPE_RESCAN_MAX", 100000),
	PreloadDepth: envInt("LOUPE_PRELOAD_DEPTH", 3), // images the rating UI prefetches ahead
	GDL:          env("LOUPE_GALLERY_DL", "gallery-dl"),
	TimeoutSec:   envInt("LOUPE_GDL_TIMEOUT_SEC", 120),
}

/* ------------------------------------------------------------------ model */

// The domain structs live in the persistence layer (internal/repo); these
// aliases keep the handler code reading the same as before the SQL migration.
type (
	Source     = repo.Source
	Collection = repo.Collection
	Item       = repo.Item
	State      = repo.State
)

// store is the database-backed data layer (SQLite by default; Postgres or MySQL
// via LOUPE_DB_DRIVER/LOUPE_DB_DSN). All reads and writes go through it — there
// is no in-memory copy of the data.
var store repo.Repo

var pollMu sync.Mutex

func now() int64 { return time.Now().UnixMilli() }

// expandPath resolves a leading ~/ to the user's home dir so source config
// paths can be entered the natural way (e.g. ~/.config/gallery-dl/booru.conf).
func expandPath(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

func uuid() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func makeID(sourceID, imageKey string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(sourceID + "\x00" + imageKey))
}

// dbFail writes a 500 and returns true when err != nil, so handlers can bail on a
// database error with one line.
func dbFail(w http.ResponseWriter, err error) bool {
	if err != nil {
		fmt.Println("db error:", err)
		writeJSON(w, 500, map[string]any{"error": "database error"})
		return true
	}
	return false
}

/* ------------------------------------------------------------- gallery-dl */

var gdlInfo = struct {
	Available bool   `json:"available"`
	Version   string `json:"version"`
}{}

func runGdl(args ...string) (stdout, stderr string, code int, missing bool) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.TimeoutSec)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cfg.GDL, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	// A timeout kill must surface as an error: gallery-dl -j prints its dump
	// only at the end of a run, so a killed run yields empty output and would
	// otherwise read as a silent "0 new items". Common on the first full scan
	// of a large HTML-paged gallery — the fix is raising LOUPE_GDL_TIMEOUT_SEC.
	if ctx.Err() == context.DeadlineExceeded {
		return out.String(),
			fmt.Sprintf("gallery-dl timed out after %ds — raise LOUPE_GDL_TIMEOUT_SEC for large/slow galleries", cfg.TimeoutSec),
			-1, false
	}
	if err != nil {
		var ee *exec.Error
		if errors.As(err, &ee) && errors.Is(ee.Err, exec.ErrNotFound) {
			return "", "", -1, true
		}
		var xe *exec.ExitError
		if errors.As(err, &xe) {
			return out.String(), errb.String(), xe.ExitCode(), false
		}
		return out.String(), errb.String(), -1, false
	}
	return out.String(), errb.String(), 0, false
}

func checkGdl() {
	out, _, code, missing := runGdl("--version")
	gdlInfo.Available = !missing && code == 0
	gdlInfo.Version = strings.TrimSpace(out)
}

type extracted struct{ url, sample, title, imageKey string }

func mstr(md map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := md[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}
func mnum(md map[string]any, keys ...string) float64 {
	for _, k := range keys {
		if f, ok := md[k].(float64); ok {
			return f
		}
	}
	return 0
}
func mkey(md map[string]any) string {
	for _, k := range []string{"md5", "id"} {
		if v, ok := md[k]; ok {
			switch t := v.(type) {
			case string:
				if t != "" {
					return t
				}
			case float64:
				return strconv.FormatFloat(t, 'f', -1, 64)
			}
		}
	}
	return ""
}

// redditPreview returns a properly-sized preview URL for a reddit submission.
// Reddit's flat `thumbnail` field is a tiny 140x140 crop; the real previews
// live elsewhere and are reddit-capped near 1080px wide — big enough for the
// rating card, far lighter than fetching the full image. Two shapes exist:
//   - single-image posts: preview.images[0] with `source` + `resolutions`
//   - gallery posts: media_metadata[<id>] with `s` + `p`, keyed by image id
//
// For galleries each emitted item carries its own i.redd.it URL, so we key off
// the URL's basename to pick *that* image's preview rather than the first one.
// Reddit serves these URLs HTML-escaped (&amp;), so the result is unescaped.
// Returns "" when no usable preview is present.
func redditPreview(md map[string]any, url string) string {
	// Gallery member: look up this image's own preview by id.
	if id := redditID(url); id != "" {
		if mm, ok := md["media_metadata"].(map[string]any); ok {
			if e, ok := mm[id].(map[string]any); ok {
				if u := bestRedditRes(e["p"], e["s"]); u != "" {
					return u
				}
			}
		}
	}
	// Single-image post.
	if prev, ok := md["preview"].(map[string]any); ok {
		if imgs, ok := prev["images"].([]any); ok && len(imgs) > 0 {
			if img, ok := imgs[0].(map[string]any); ok {
				if u := bestRedditRes(img["resolutions"], img["source"]); u != "" {
					return u
				}
			}
		}
	}
	return ""
}

// bestRedditRes picks the widest URL from a reddit resolutions list, falling
// back to the full source. It handles both key spellings reddit uses:
// preview.images entries are {url,width}, media_metadata entries are {u,x}.
// The chosen URL is HTML-unescaped.
func bestRedditRes(resolutions, source any) string {
	best, bestW := "", -1.0
	if list, ok := resolutions.([]any); ok {
		for _, r := range list {
			rm, ok := r.(map[string]any)
			if !ok {
				continue
			}
			if u := mstr(rm, "url", "u"); u != "" {
				if w := mnum(rm, "width", "x"); w > bestW {
					best, bestW = u, w
				}
			}
		}
	}
	if best == "" {
		if sm, ok := source.(map[string]any); ok {
			best = mstr(sm, "url", "u")
		}
	}
	return html.UnescapeString(best)
}

// redditID extracts the media id from an i.redd.it URL — the basename without
// query, fragment, or extension — which is also the key into media_metadata.
func redditID(url string) string {
	base := url[strings.LastIndex(url, "/")+1:]
	if i := strings.IndexAny(base, "?#"); i >= 0 {
		base = base[:i]
	}
	if i := strings.LastIndex(base, "."); i >= 0 {
		base = base[:i]
	}
	return base
}

func parseDump(s string) []extracted {
	var raw []json.RawMessage
	if json.Unmarshal([]byte(s), &raw) != nil {
		return nil
	}
	var strict, loose []extracted
	for _, r := range raw {
		var m []json.RawMessage
		if json.Unmarshal(r, &m) != nil || len(m) < 2 {
			continue
		}
		var typ int
		_ = json.Unmarshal(m[0], &typ)
		var url string
		_ = json.Unmarshal(m[1], &url)
		if url == "" || !strings.HasPrefix(strings.ToLower(url), "http") {
			continue
		}
		var md map[string]any
		if len(m) >= 3 {
			_ = json.Unmarshal(m[2], &md)
		}
		ik := mkey(md)
		if ik == "" {
			ik = url
		}
		// Preview priority: booru sample/preview fields, then reddit's sized
		// preview, then the small thumbnail, finally the full image itself.
		sample := mstr(md, "sample_url", "preview_url")
		if sample == "" {
			sample = redditPreview(md, url)
		}
		if sample == "" {
			sample = pick(mstr(md, "thumbnail", "thumbnail_url"), url)
		}
		e := extracted{
			url:      url,
			sample:   sample,
			title:    trunc(mstr(md, "title", "tags", "filename"), 120),
			imageKey: ik,
		}
		if typ == 3 {
			strict = append(strict, e)
		} else if typ != 2 && typ != 6 {
			loose = append(loose, e)
		}
	}
	if len(strict) > 0 {
		return strict
	}
	return loose
}

// parseError pulls a human-readable reason out of a gallery-dl -j dump. On
// failures (auth required, rate limit, private gallery, …) gallery-dl emits a
// message of type -1 — `[-1, {"error":..., "message":...}]` — and still exits 0
// with empty stderr, so without this the poll would look like a silent "0 new
// items, no error". Returns "" when no error message is present.
func parseError(s string) string {
	var raw []json.RawMessage
	if json.Unmarshal([]byte(s), &raw) != nil {
		return ""
	}
	for _, r := range raw {
		var m []json.RawMessage
		if json.Unmarshal(r, &m) != nil || len(m) < 2 {
			continue
		}
		var typ int
		_ = json.Unmarshal(m[0], &typ)
		if typ != -1 {
			continue
		}
		var e struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		if json.Unmarshal(m[1], &e) == nil {
			switch {
			case e.Error != "" && e.Message != "":
				return trunc(e.Error+": "+e.Message, 200)
			case e.Message != "":
				return trunc(e.Message, 200)
			case e.Error != "":
				return trunc(e.Error, 200)
			}
		}
	}
	return ""
}

func pick(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return trunc(l, 200)
		}
	}
	return ""
}

/* ------------------------------------------------------------- polling */

func setPollError(id, msg string) {
	if err := store.SetSourceError(context.Background(), id, msg, now()); err != nil {
		fmt.Println("db error (setPollError):", err)
	}
}

// gdlPageSize is the per-request window for an exhaustive rescan: gallery-dl
// pages through up to this many items per --range invocation, and we keep
// requesting further windows until the source runs out (or cfg.RescanMax).
const gdlPageSize = 1000

// buildConfigArgs assembles gallery-dl -c flags for a source's shared config
// (by id), config file, and inline JSON (the JSON bodies staged to temp files).
// Per-source config *adds* to gallery-dl's default config files; they apply in
// order (later overrides earlier), so we layer: defaults -> shared config ->
// file -> inline JSON (most specific wins). The shared config is resolved from
// the store on every poll, so editing it propagates to every referencing source.
// The returned cleanup must always be called; errMsg is non-empty if unusable.
func buildConfigArgs(cfgFile, cfgJSON, cfgID string) (pre []string, cleanup func(), errMsg string) {
	var tmps []string
	cleanup = func() {
		for _, n := range tmps {
			os.Remove(n)
		}
	}
	// stage writes a JSON body to a temp file and records it for cleanup.
	stage := func(body string) (string, error) {
		tmp, err := os.CreateTemp("", "loupe-cfg-*.json")
		if err != nil {
			return "", err
		}
		name := tmp.Name()
		_, werr := tmp.WriteString(body)
		tmp.Close()
		if werr != nil {
			os.Remove(name)
			return "", werr
		}
		tmps = append(tmps, name)
		return name, nil
	}

	// Shared config first: it is the common base that the source's own file and
	// inline JSON then refine.
	if cfgID != "" {
		c, ok, err := store.GetConfig(context.Background(), cfgID)
		if err != nil {
			return nil, cleanup, "could not load shared config: " + err.Error()
		}
		if !ok {
			return nil, cleanup, "shared config not found (was it deleted?)"
		}
		if strings.TrimSpace(c.ConfigJSON) != "" {
			name, err := stage(c.ConfigJSON)
			if err != nil {
				return nil, cleanup, "could not stage shared config: " + err.Error()
			}
			pre = append(pre, "-c", name)
		}
	}
	if cfgFile != "" {
		path := expandPath(cfgFile)
		if _, err := os.Stat(path); err != nil {
			return nil, cleanup, "config file not found: " + cfgFile
		}
		pre = append(pre, "-c", path)
	}
	if cfgJSON != "" {
		name, err := stage(cfgJSON)
		if err != nil {
			return nil, cleanup, "could not stage inline config: " + err.Error()
		}
		pre = append(pre, "-c", name)
	}
	return pre, cleanup, ""
}

// pageError returns a user-facing message if a gallery-dl page failed, else "".
// gallery-dl fails two ways: an in-band [-1,{error}] message (exit 0, empty
// stderr — e.g. auth required) or a non-zero exit with a stderr message.
func pageError(out, errs string, code int) string {
	if msg := parseError(out); msg != "" {
		return msg
	}
	if code != 0 {
		return lastLine(errs)
	}
	return ""
}

// mergeExtracted inserts any new items for a source and refreshes (un-stales)
// ones still present. Returns how many were newly added. The whole batch runs in
// one transaction so a mid-poll failure leaves the store consistent.
func mergeExtracted(id, label string, items []extracted) (added int) {
	ctx := context.Background()
	if _, found, err := store.GetSource(ctx, id); err != nil || !found {
		return 0
	}
	err := store.WithinTx(ctx, func(tx repo.Repo) error {
		for _, it := range items {
			itemID := makeID(id, it.imageKey) // per-source key
			ins, err := tx.InsertItemIfNew(ctx, &Item{ID: itemID, SourceID: id, Label: label, ImageKey: it.imageKey,
				URL: it.url, Sample: it.sample, Title: it.title, Status: "new", AddedAt: now(), LastSeen: now()})
			if err != nil {
				return err
			}
			if ins {
				added++
				continue
			}
			if err := tx.UnstaleItem(ctx, itemID, now()); err != nil { // still present → not stale
				return err
			}
		}
		return nil
	})
	if err != nil {
		fmt.Println("db error (mergeExtracted):", err)
		return 0
	}
	return added
}

func markPolled(id string, added int) {
	if err := store.MarkPolled(context.Background(), id, now(), added); err != nil {
		fmt.Println("db error (markPolled):", err)
	}
}

// pollSourceSnapshot fetches the newest cfg.PerSource items in a single window —
// used by the scheduled poll and the initial add for a fast first batch.
func pollSourceSnapshot(id, url, label, cfgFile, cfgJSON, cfgID string) (added int, missing bool) {
	pre, cleanup, errMsg := buildConfigArgs(cfgFile, cfgJSON, cfgID)
	defer cleanup()
	if errMsg != "" {
		setPollError(id, errMsg)
		return 0, false
	}
	args := append(append([]string{}, pre...), "-j", "--range", "1-"+strconv.Itoa(cfg.PerSource), url)
	out, errs, code, miss := runGdl(args...)
	if miss {
		setPollError(id, "gallery-dl not installed")
		return 0, true
	}
	items := parseDump(out)
	if len(items) == 0 {
		if msg := pageError(out, errs, code); msg != "" {
			setPollError(id, msg)
			return 0, false
		}
	}
	added = mergeExtracted(id, label, items)
	markPolled(id, added)
	return added, false
}

// pollSourceFull scans the *entire* source, paging through gallery-dl in windows
// of gdlPageSize until a short page signals the end (or cfg.RescanMax is hit).
// Used by the manual rescan. Items are merged per page, so a later-page failure
// still keeps everything fetched so far.
func pollSourceFull(id, url, label, cfgFile, cfgJSON, cfgID string) (added int, missing bool) {
	pre, cleanup, errMsg := buildConfigArgs(cfgFile, cfgJSON, cfgID)
	defer cleanup()
	if errMsg != "" {
		setPollError(id, errMsg)
		return 0, false
	}
	total := 0
	for off := 0; off < cfg.RescanMax; off += gdlPageSize {
		rng := strconv.Itoa(off+1) + "-" + strconv.Itoa(off+gdlPageSize)
		args := append(append([]string{}, pre...), "-j", "--range", rng, url)
		out, errs, code, miss := runGdl(args...)
		if miss {
			setPollError(id, "gallery-dl not installed")
			return total, true
		}
		items := parseDump(out)
		if len(items) == 0 {
			// A failure on the very first page is a real error; on a later page an
			// empty/erroring window just means we ran off the end — stop and keep
			// what we already have.
			if off == 0 {
				if msg := pageError(out, errs, code); msg != "" {
					setPollError(id, msg)
					return 0, false
				}
			}
			break
		}
		total += mergeExtracted(id, label, items)
		if len(items) < gdlPageSize {
			break // last, partial page → end of the gallery
		}
	}
	markPolled(id, total)
	return total, false
}

// pollAsync kicks off a background poll for one source. It's a package var so
// tests can replace it with a no-op — otherwise the spawned goroutine would
// race with later test mutations of the shared store/database.
var pollAsync = func(id string) { go pollOne(id) }

func pollOne(id string) {
	s, found, err := store.GetSource(context.Background(), id)
	if err != nil || !found || s.URL == "" {
		return
	}
	pollSourceSnapshot(id, s.URL, s.Name, s.ConfigFile, s.ConfigJSON, s.ConfigID)
}

// pollAsyncFull kicks off a background *full* scan for one source — paging the
// entire gallery, not just the newest window. Used when a source is first added
// so the whole backlog is ingested up front. Like pollAsync it's a package var
// so tests can replace it with a no-op.
var pollAsyncFull = func(id string) { go pollOneFull(id) }

func pollOneFull(id string) {
	s, found, err := store.GetSource(context.Background(), id)
	if err != nil || !found || s.URL == "" {
		return
	}
	pollSourceFull(id, s.URL, s.Name, s.ConfigFile, s.ConfigJSON, s.ConfigID)
}

func pollAll() (added int, missing bool) {
	if !pollMu.TryLock() {
		return 0, false
	}
	defer pollMu.Unlock()

	ctx := context.Background()
	sources, err := store.ListSources(ctx)
	if err != nil {
		fmt.Println("db error (pollAll):", err)
		return 0, false
	}
	for _, sp := range sources {
		a, miss := pollSourceSnapshot(sp.ID, sp.URL, sp.Name, sp.ConfigFile, sp.ConfigJSON, sp.ConfigID)
		added += a
		if miss {
			missing = true
		}
		time.Sleep(800 * time.Millisecond)
	}
	if err := store.SetState(ctx, State{LastPoll: now(), LastAdded: added}); err != nil {
		fmt.Println("db error (pollAll state):", err)
	}
	return added, missing
}

/* ------------------------------------------------------------- http utils */

// hostname strips the port (and IPv6 brackets) from a Host header value.
func hostname(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return strings.Trim(hostport, "[]")
}

// hostAllowed decides whether a request's Host header names this server. IP
// literals and localhost are always fine: a DNS-rebinding page can't reach the
// server under those (the browser sends the *attacker's* hostname as Host).
// Anything else — a LAN hostname, a reverse-proxy domain — must be listed in
// LOUPE_ALLOWED_HOSTS.
func hostAllowed(host string, extra map[string]bool) bool {
	h := strings.ToLower(host)
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	if net.ParseIP(h) != nil {
		return true
	}
	return extra[h]
}

// guard is the security middleware in front of every route. It blocks
// DNS-rebinding (foreign Host header) on all requests, and cross-site request
// forgery on mutations: every POST must carry a JSON content type (an HTML form
// can't) and, when the browser supplies an Origin, its host must match the
// request's Host.
func guard(next http.Handler) http.Handler {
	extra := map[string]bool{}
	for _, h := range strings.Split(env("LOUPE_ALLOWED_HOSTS", ""), ",") {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			extra[h] = true
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hostAllowed(hostname(r.Host), extra) {
			writeJSON(w, 403, map[string]any{"error": "host not allowed — set LOUPE_ALLOWED_HOSTS"})
			return
		}
		if r.Method == http.MethodPost {
			if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(ct), "application/json") {
				writeJSON(w, 415, map[string]any{"error": "Content-Type must be application/json"})
				return
			}
			// The Origin must be this server: same as the Host, or itself an
			// allowed host. The latter covers proxies that rewrite Host to the
			// backend's (Vite dev on :5173, a reverse proxy in front) — the page
			// still originates from an address the operator has sanctioned.
			if o := r.Header.Get("Origin"); o != "" && o != "null" {
				u, err := url.Parse(o)
				if err != nil || (!strings.EqualFold(u.Host, r.Host) && !hostAllowed(u.Hostname(), extra)) {
					writeJSON(w, 403, map[string]any{"error": "cross-origin request rejected"})
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// postOnly rejects any non-POST method on a mutating endpoint.
func postOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, 405, map[string]any{"error": "POST required"})
			return
		}
		h(w, r)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func readJSON(r *http.Request) map[string]any {
	m := map[string]any{}
	_ = json.NewDecoder(r.Body).Decode(&m)
	return m
}
func str(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

// validateConfigJSON returns "" if s is empty or a valid, reasonably-sized
// gallery-dl config object; otherwise a user-facing error message.
func validateConfigJSON(s string) string {
	if s == "" {
		return ""
	}
	if len(s) > 100000 {
		return "config JSON is too large"
	}
	var v any
	if json.Unmarshal([]byte(s), &v) != nil {
		return "config is not valid JSON"
	}
	return ""
}

func newCounts() map[string]int {
	return map[string]int{"new": 0, "good": 0, "bad": 0, "gone": 0, "staleNew": 0}
}

// bumpCount tallies n items sharing a (status, gone) into c: stale items count
// toward "gone"; only present, undecided items count as "new"; decided items
// count as good/bad regardless of stale state. "staleNew" tracks
// orphaned-but-undecided items (reviewable in stale mode). This is the single
// source of truth for the count semantics, fed both per-item and from GROUP BY
// rows.
func bumpCount(c map[string]int, status string, gone bool, n int) {
	if gone {
		c["gone"] += n
		if status == "new" {
			c["staleNew"] += n
		}
	}
	switch {
	case status == "new" && !gone:
		c["new"] += n
	case status == "good":
		c["good"] += n
	case status == "bad":
		c["bad"] += n
	}
}

func bump(c map[string]int, it *Item) { bumpCount(c, it.Status, it.Gone, 1) }

// sourceNames returns a sourceID -> current name map for resolving item labels.
func sourceNames(ctx context.Context) (map[string]string, error) {
	srcs, err := store.ListSources(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(srcs))
	for _, s := range srcs {
		m[s.ID] = s.Name
	}
	return m, nil
}

// itemDTO resolves the current source name (falling back to the item's stored
// label) for display.
func itemDTO(it *Item, names map[string]string) map[string]any {
	name := it.Label
	if n, ok := names[it.SourceID]; ok {
		name = n
	}
	return map[string]any{"id": it.ID, "sourceId": it.SourceID, "label": name,
		"url": it.URL, "sample": it.Sample, "title": it.Title, "status": it.Status, "gone": it.Gone}
}

/* ------------------------------------------------------------- handlers */

func handleStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	counts := newCounts()
	rows, err := store.CountByStatusGone(ctx)
	if dbFail(w, err) {
		return
	}
	total := 0
	for _, row := range rows {
		bumpCount(counts, row.Status, row.Gone, row.N)
		total += row.N
	}
	st, err := store.GetState(ctx)
	if dbFail(w, err) {
		return
	}
	srcs, err := store.ListSources(ctx)
	if dbFail(w, err) {
		return
	}
	cols, err := store.ListCollections(ctx)
	if dbFail(w, err) {
		return
	}
	writeJSON(w, 200, map[string]any{
		"counts": counts, "total": total,
		"lastPoll": st.LastPoll, "lastAdded": st.LastAdded,
		"sources": len(srcs), "collections": len(cols),
		"pollMinutes": cfg.PollMin, "preloadDepth": cfg.PreloadDepth, "gdl": gdlInfo,
	})
}

func handleSources(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method == "POST" {
		b := readJSON(r)
		url := strings.TrimSpace(str(b, "url"))
		if !strings.HasPrefix(strings.ToLower(url), "http") {
			writeJSON(w, 400, map[string]any{"error": "need an http(s) gallery URL"})
			return
		}
		name := strings.TrimSpace(str(b, "name"))
		if name == "" {
			name = url
		}
		cfgJSON := strings.TrimSpace(str(b, "configJson"))
		if msg := validateConfigJSON(cfgJSON); msg != "" {
			writeJSON(w, 400, map[string]any{"error": msg})
			return
		}
		id := uuid()
		err := store.InsertSource(ctx, &Source{ID: id, Name: trunc(name, 80),
			Description: trunc(str(b, "description"), 400), URL: url,
			ConfigFile: trunc(strings.TrimSpace(str(b, "configFile")), 400),
			ConfigJSON: cfgJSON, ConfigID: strings.TrimSpace(str(b, "configId")), AddedAt: now()})
		if dbFail(w, err) {
			return
		}
		pollAsyncFull(id) // first add: scan the whole gallery, not just the newest window
		writeJSON(w, 200, map[string]any{"ok": true, "id": id})
		return
	}

	srcs, err := store.ListSources(ctx)
	if dbFail(w, err) {
		return
	}
	srcCounts, err := store.CountBySourceStatusGone(ctx)
	if dbFail(w, err) {
		return
	}
	cols, err := store.ListCollections(ctx)
	if dbFail(w, err) {
		return
	}
	counts := map[string]map[string]int{}
	for _, row := range srcCounts {
		c := counts[row.SourceID]
		if c == nil {
			c = newCounts()
			counts[row.SourceID] = c
		}
		bumpCount(c, row.Status, row.Gone, row.N)
	}
	inCol := map[string][]map[string]any{}
	for _, col := range cols {
		for _, sid := range col.SourceIDs {
			inCol[sid] = append(inCol[sid], map[string]any{"id": col.ID, "name": col.Name})
		}
	}
	out := make([]map[string]any, 0, len(srcs))
	for _, s := range srcs {
		c := counts[s.ID]
		if c == nil {
			c = newCounts()
		}
		out = append(out, map[string]any{"id": s.ID, "name": s.Name, "description": s.Description,
			"url": s.URL, "configFile": s.ConfigFile, "configJson": s.ConfigJSON, "configId": s.ConfigID,
			"lastPoll": s.LastPoll, "lastError": s.LastError,
			"counts": c, "collections": inCol[s.ID]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["name"].(string) < out[j]["name"].(string) })
	writeJSON(w, 200, out)
}

func handleSourceUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	b := readJSON(r)
	id := str(b, "id")
	if _, ok := b["configJson"]; ok {
		if msg := validateConfigJSON(strings.TrimSpace(str(b, "configJson"))); msg != "" {
			writeJSON(w, 400, map[string]any{"error": msg})
			return
		}
	}
	s, found, err := store.GetSource(ctx, id)
	if dbFail(w, err) {
		return
	}
	urlChanged := false
	if found {
		if v := strings.TrimSpace(str(b, "name")); v != "" {
			s.Name = trunc(v, 80)
		}
		if _, ok := b["description"]; ok {
			s.Description = trunc(str(b, "description"), 400)
		}
		if _, ok := b["configFile"]; ok { // presence-based so it can be cleared
			s.ConfigFile = trunc(strings.TrimSpace(str(b, "configFile")), 400)
		}
		if _, ok := b["configJson"]; ok { // presence-based so it can be cleared
			s.ConfigJSON = strings.TrimSpace(str(b, "configJson"))
		}
		if _, ok := b["configId"]; ok { // presence-based so it can be cleared
			s.ConfigID = strings.TrimSpace(str(b, "configId"))
		}
		if nu := strings.TrimSpace(str(b, "url")); nu != "" && strings.HasPrefix(strings.ToLower(nu), "http") && nu != s.URL {
			s.URL = nu
			urlChanged = true
		}
		if dbFail(w, store.UpdateSource(ctx, s)) {
			return
		}
		// Items came from the OLD url. Mark them stale (not deleted); the re-poll
		// below revives any that still appear under the new url.
		if urlChanged {
			if dbFail(w, store.MarkSourceGone(ctx, id)) {
				return
			}
			pollAsync(id)
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true, "urlChanged": urlChanged})
}

// Rescan one source on demand (the "rescan" button). Synchronous, so the UI can
// report how many new items appeared — or surface why the poll failed.
func handleSourceRescan(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := str(readJSON(r), "id")
	s, found, err := store.GetSource(ctx, id)
	if dbFail(w, err) {
		return
	}
	if !found || s.URL == "" {
		writeJSON(w, 404, map[string]any{"error": "unknown source"})
		return
	}
	added, missing := pollSourceFull(id, s.URL, s.Name, s.ConfigFile, s.ConfigJSON, s.ConfigID)
	lastErr := ""
	if cur, ok, _ := store.GetSource(ctx, id); ok {
		lastErr = cur.LastError
	}
	writeJSON(w, 200, map[string]any{"ok": true, "added": added, "missing": missing, "lastError": lastErr})
}

// Delete a source's stale items — but keep decided keepers (good) as history.
func handleSourcePurgeStale(w http.ResponseWriter, r *http.Request) {
	id := str(readJSON(r), "id")
	n, err := store.PurgeStale(r.Context(), id)
	if dbFail(w, err) {
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "purged": n})
}

func handleSourceRemove(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := str(readJSON(r), "id")
	if dbFail(w, store.DeleteSource(ctx, id)) {
		return
	}
	if dbFail(w, store.DeleteNewBySource(ctx, id)) {
		return
	}
	cols, err := store.ListCollections(ctx)
	if dbFail(w, err) {
		return
	}
	for _, col := range cols {
		var nl []string
		removed := false
		for _, x := range col.SourceIDs {
			if x == id {
				removed = true
				continue
			}
			nl = append(nl, x)
		}
		if removed {
			col.SourceIDs = nl
			if dbFail(w, store.UpdateCollection(ctx, col)) {
				return
			}
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func handleCollections(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method == "POST" {
		b := readJSON(r)
		name := strings.TrimSpace(str(b, "name"))
		if name == "" {
			writeJSON(w, 400, map[string]any{"error": "name required"})
			return
		}
		id := uuid()
		err := store.InsertCollection(ctx, &Collection{ID: id, Name: trunc(name, 80),
			Description: trunc(str(b, "description"), 400), SourceIDs: []string{}, AddedAt: now()})
		if dbFail(w, err) {
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "id": id})
		return
	}

	cols, err := store.ListCollections(ctx)
	if dbFail(w, err) {
		return
	}
	srcCounts, err := store.CountBySourceStatusGone(ctx)
	if dbFail(w, err) {
		return
	}
	srcs, err := store.ListSources(ctx)
	if dbFail(w, err) {
		return
	}
	names := make(map[string]string, len(srcs))
	for _, s := range srcs {
		names[s.ID] = s.Name
	}
	cnt := map[string]map[string]int{}
	for _, row := range srcCounts {
		c := cnt[row.SourceID]
		if c == nil {
			c = newCounts()
			cnt[row.SourceID] = c
		}
		bumpCount(c, row.Status, row.Gone, row.N)
	}
	out := make([]map[string]any, 0, len(cols))
	for _, col := range cols {
		agg := newCounts()
		members := []map[string]any{}
		for _, sid := range col.SourceIDs {
			name, ok := names[sid]
			if !ok {
				continue
			}
			if c := cnt[sid]; c != nil {
				agg["new"] += c["new"]
				agg["good"] += c["good"]
				agg["bad"] += c["bad"]
				agg["gone"] += c["gone"]
				agg["staleNew"] += c["staleNew"]
			}
			members = append(members, map[string]any{"id": sid, "name": name})
		}
		out = append(out, map[string]any{"id": col.ID, "name": col.Name, "description": col.Description,
			"sourceIds": col.SourceIDs, "members": members, "counts": agg})
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["name"].(string) < out[j]["name"].(string) })
	writeJSON(w, 200, out)
}

func handleCollectionRemove(w http.ResponseWriter, r *http.Request) {
	id := str(readJSON(r), "id")
	if dbFail(w, store.DeleteCollection(r.Context(), id)) {
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// handleConfigs lists the shared gallery-dl configs (GET) or creates one (POST).
// A config is just a name + JSON body that sources reference by id.
func handleConfigs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method == "POST" {
		b := readJSON(r)
		name := strings.TrimSpace(str(b, "name"))
		if name == "" {
			writeJSON(w, 400, map[string]any{"error": "name required"})
			return
		}
		cfgJSON := strings.TrimSpace(str(b, "configJson"))
		if msg := validateConfigJSON(cfgJSON); msg != "" {
			writeJSON(w, 400, map[string]any{"error": msg})
			return
		}
		id := uuid()
		if dbFail(w, store.InsertConfig(ctx, &repo.Config{ID: id, Name: trunc(name, 80), ConfigJSON: cfgJSON, AddedAt: now()})) {
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "id": id})
		return
	}

	cfgs, err := store.ListConfigs(ctx)
	if dbFail(w, err) {
		return
	}
	// How many sources reference each config, so the UI can warn before deleting.
	srcs, err := store.ListSources(ctx)
	if dbFail(w, err) {
		return
	}
	uses := map[string]int{}
	for _, s := range srcs {
		if s.ConfigID != "" {
			uses[s.ConfigID]++
		}
	}
	out := make([]map[string]any, 0, len(cfgs))
	for _, c := range cfgs {
		out = append(out, map[string]any{"id": c.ID, "name": c.Name, "configJson": c.ConfigJSON, "uses": uses[c.ID]})
	}
	writeJSON(w, 200, out)
}

// handleConfigUpdate edits a shared config in place; the new body is used by
// every referencing source on its next poll (configs are resolved, not copied).
func handleConfigUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	b := readJSON(r)
	id := str(b, "id")
	c, found, err := store.GetConfig(ctx, id)
	if dbFail(w, err) {
		return
	}
	if !found {
		writeJSON(w, 404, map[string]any{"error": "unknown config"})
		return
	}
	if v := strings.TrimSpace(str(b, "name")); v != "" {
		c.Name = trunc(v, 80)
	}
	if _, ok := b["configJson"]; ok {
		cfgJSON := strings.TrimSpace(str(b, "configJson"))
		if msg := validateConfigJSON(cfgJSON); msg != "" {
			writeJSON(w, 400, map[string]any{"error": msg})
			return
		}
		c.ConfigJSON = cfgJSON
	}
	if dbFail(w, store.UpdateConfig(ctx, c)) {
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// handleConfigRemove deletes a shared config. Sources that referenced it keep
// their config_id pointing at the now-missing config; the next poll surfaces a
// "shared config not found" error rather than silently dropping the config — so
// the reference is a visible loose end the user can fix, not a silent change.
func handleConfigRemove(w http.ResponseWriter, r *http.Request) {
	id := str(readJSON(r), "id")
	if dbFail(w, store.DeleteConfig(r.Context(), id)) {
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func handleCollectionMember(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	b := readJSON(r)
	cid, sid, action := str(b, "collectionId"), str(b, "sourceId"), str(b, "action")
	col, found, err := store.GetCollection(ctx, cid)
	if dbFail(w, err) {
		return
	}
	if found {
		_, srcOK, err := store.GetSource(ctx, sid)
		if dbFail(w, err) {
			return
		}
		if srcOK {
			var nl []string
			for _, x := range col.SourceIDs {
				if x != sid {
					nl = append(nl, x)
				}
			}
			if action == "add" {
				nl = append(nl, sid)
			}
			col.SourceIDs = nl
			if dbFail(w, store.UpdateCollection(ctx, col)) {
				return
			}
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// interleaveBySource round-robins items across their sources so a multi-source
// scope (a collection, or "all") mixes sources instead of showing one source's
// entire backlog before the next — important after a big rescan dumps thousands
// of items into one source. Within a source, items stay newest-first; the
// source whose newest item is most recent leads each round (stable by id).
func interleaveBySource(items []*Item) []*Item {
	groups := map[string][]*Item{}
	var order []string
	for _, it := range items {
		if _, ok := groups[it.SourceID]; !ok {
			order = append(order, it.SourceID)
		}
		groups[it.SourceID] = append(groups[it.SourceID], it)
	}
	for sid := range groups {
		g := groups[sid]
		sort.Slice(g, func(i, j int) bool { return g[i].AddedAt > g[j].AddedAt })
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := groups[order[i]][0], groups[order[j]][0]
		if a.AddedAt != b.AddedAt {
			return a.AddedAt > b.AddedAt
		}
		return order[i] < order[j]
	})
	out := make([]*Item, 0, len(items))
	for i := 0; ; i++ {
		progressed := false
		for _, sid := range order {
			if g := groups[sid]; i < len(g) {
				out = append(out, g[i])
				progressed = true
			}
		}
		if !progressed {
			break
		}
	}
	return out
}

// scopeSourceFilter resolves a scope string to the set of source ids it admits.
// Returns nil for "all" (and any scope without a source/collection prefix, e.g.
// "stale"), meaning "no source filtering". A "collection:<id>" that resolves to
// no sources yields an empty (non-nil) set — i.e. it matches nothing.
func scopeSourceFilter(ctx context.Context, scope string) map[string]bool {
	if strings.HasPrefix(scope, "source:") {
		return map[string]bool{strings.TrimPrefix(scope, "source:"): true}
	}
	if strings.HasPrefix(scope, "collection:") {
		f := map[string]bool{}
		if c, ok, err := store.GetCollection(ctx, strings.TrimPrefix(scope, "collection:")); err == nil && ok {
			for _, sid := range c.SourceIDs {
				f[sid] = true
			}
		}
		return f
	}
	return nil
}

// filterByScope keeps only items whose source is admitted by the scope. "all" /
// "" / "stale" pass everything through. Used by the selects views so a scoped
// Selects page (and its exports) mirrors the same source/collection filter the
// review queue uses.
func filterByScope(ctx context.Context, scope string, items []*Item) []*Item {
	f := scopeSourceFilter(ctx, scope)
	if f == nil {
		return items
	}
	out := make([]*Item, 0, len(items))
	for _, it := range items {
		if f[it.SourceID] {
			out = append(out, it)
		}
	}
	return out
}

func handleNext(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "all"
	}
	stale := scope == "stale"           // review orphaned-but-undecided items
	first := r.URL.Query().Get("first") // deep-link: pin this item to the front
	count := envIntQ(r, "count", 30)

	filter := scopeSourceFilter(ctx, scope) // nil => all sources

	var candidates []*Item
	var err error
	if stale {
		candidates, err = store.ListStaleNew(ctx)
	} else {
		candidates, err = store.ListReviewable(ctx)
	}
	if dbFail(w, err) {
		return
	}

	var items []*Item
	for _, it := range candidates {
		if !stale && filter != nil && !filter[it.SourceID] {
			continue
		}
		items = append(items, it)
	}
	items = interleaveBySource(items)

	// Deep link (/review/{id}): if that item is still reviewable, surface it first
	// regardless of scope, so a shared/bookmarked link lands on the right card.
	if first != "" {
		if it, ok, _ := store.GetItem(ctx, first); ok && it.Status == "new" && !it.Gone {
			pinned := make([]*Item, 0, len(items)+1)
			pinned = append(pinned, it)
			for _, x := range items {
				if x.ID != first {
					pinned = append(pinned, x)
				}
			}
			items = pinned
		}
	}
	if len(items) > count {
		items = items[:count]
	}

	names, err := sourceNames(ctx)
	if dbFail(w, err) {
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		out = append(out, itemDTO(it, names))
	}
	writeJSON(w, 200, out)
}

func envIntQ(r *http.Request, k string, d int) int {
	if v := r.URL.Query().Get(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			if n > 100 {
				n = 100
			}
			return n
		}
	}
	return d
}

func handleVote(w http.ResponseWriter, r *http.Request) {
	b := readJSON(r)
	id, dec := str(b, "id"), str(b, "decision")
	if dec != "good" && dec != "bad" {
		writeJSON(w, 400, map[string]any{"error": "decision must be good|bad"})
		return
	}
	found, err := store.DecideItem(r.Context(), id, dec, now())
	if dbFail(w, err) {
		return
	}
	if !found {
		writeJSON(w, 404, map[string]any{"error": "unknown id"})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// Undo: put a decided item back to "new" so it returns to the review queue.
func handleUnvote(w http.ResponseWriter, r *http.Request) {
	id := str(readJSON(r), "id")
	if _, err := store.ResetItem(r.Context(), id); dbFail(w, err) {
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func handleSelects(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	items, err := store.ListGood(ctx)
	if dbFail(w, err) {
		return
	}
	items = filterByScope(ctx, r.URL.Query().Get("scope"), items)
	names, err := sourceNames(ctx)
	if dbFail(w, err) {
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		out = append(out, itemDTO(it, names))
	}
	writeJSON(w, 200, out)
}

func handleUnselect(w http.ResponseWriter, r *http.Request) {
	id := str(readJSON(r), "id")
	if _, err := store.SetItemStatus(r.Context(), id, "bad"); dbFail(w, err) {
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func handlePoll(w http.ResponseWriter, r *http.Request) {
	added, missing := pollAll()
	writeJSON(w, 200, map[string]any{"added": added, "missing": missing})
}

func handleSelectsTxt(w http.ResponseWriter, r *http.Request) {
	items, err := store.ListGood(r.Context())
	if err != nil {
		http.Error(w, "db error", 500)
		return
	}
	items = filterByScope(r.Context(), r.URL.Query().Get("scope"), items)
	var urls []string
	for _, it := range items {
		urls = append(urls, it.URL)
	}
	w.Header().Set("Content-Type", "text/plain")
	io.WriteString(w, strings.Join(urls, "\n"))
}

func handleSelectsZip(w http.ResponseWriter, r *http.Request) {
	good, err := store.ListGood(r.Context())
	if err != nil {
		http.Error(w, "db error", 500)
		return
	}
	good = filterByScope(r.Context(), r.URL.Query().Get("scope"), good)
	if len(good) == 0 {
		http.Error(w, "no selects", 200)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="loupe-selects.zip"`)
	zw := zip.NewWriter(w)
	defer zw.Close()
	used := map[string]int{}
	client := &http.Client{Timeout: 60 * time.Second}
	for _, it := range good {
		resp, err := client.Get(it.URL)
		if err != nil || resp.StatusCode != 200 {
			if resp != nil {
				resp.Body.Close()
			}
			continue
		}
		name := zipEntryName(it.URL, it.ImageKey)
		if used[name] > 0 {
			name = fmt.Sprintf("%d_%s", used[name], name)
		}
		used[name]++
		if fw, err := zw.Create(name); err == nil {
			io.Copy(fw, resp.Body)
		}
		resp.Body.Close()
	}
}

// zipEntryName derives a safe archive filename from an item URL. The URL came
// from a gallery-dl dump of a remote site, so treat it as untrusted: keep only
// the basename, neutralise backslashes (a `..\..\x` entry is zip-slip against
// naive Windows extractors), and fall back to the image key when nothing
// usable remains.
func zipEntryName(rawURL, imageKey string) string {
	name := filepath.Base(strings.SplitN(rawURL, "?", 2)[0])
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.Trim(name, ". ")
	if name == "" || name == "/" {
		name = imageKey + ".jpg"
	}
	return name
}

/* ------------------------------------------------------------- import */

// runImport transfers a legacy data/store.json into the configured database. It
// is idempotent: sources/collections are upserted and items skip-if-existing, so
// re-running never duplicates or errors.
func runImport(path string) {
	var err error
	store, err = repo.Open(env("LOUPE_DB_DRIVER", ""), env("LOUPE_DB_DSN", ""))
	if err != nil {
		fmt.Println("db open:", err)
		os.Exit(1)
	}
	defer store.Close()

	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Println("read", path, "-", err)
		os.Exit(1)
	}
	var js struct {
		Sources     map[string]*Source     `json:"sources"`
		Collections map[string]*Collection `json:"collections"`
		Items       map[string]*Item       `json:"items"`
		State       struct {
			LastPoll  int64 `json:"lastPoll"`
			LastAdded int   `json:"lastAdded"`
		} `json:"state"`
	}
	if err := json.Unmarshal(b, &js); err != nil {
		fmt.Println("parse", path, "-", err)
		os.Exit(1)
	}

	ctx := context.Background()
	var ns, nc, ni int
	err = store.WithinTx(ctx, func(tx repo.Repo) error {
		for _, s := range js.Sources {
			if _, found, err := tx.GetSource(ctx, s.ID); err != nil {
				return err
			} else if found {
				if err := tx.UpdateSource(ctx, s); err != nil {
					return err
				}
			} else if err := tx.InsertSource(ctx, s); err != nil {
				return err
			}
			ns++
		}
		for _, c := range js.Collections {
			if c.SourceIDs == nil {
				c.SourceIDs = []string{}
			}
			if _, found, err := tx.GetCollection(ctx, c.ID); err != nil {
				return err
			} else if found {
				if err := tx.UpdateCollection(ctx, c); err != nil {
					return err
				}
			} else if err := tx.InsertCollection(ctx, c); err != nil {
				return err
			}
			nc++
		}
		for _, it := range js.Items {
			ins, err := tx.InsertItemIfNew(ctx, it)
			if err != nil {
				return err
			}
			if ins {
				ni++
			}
		}
		return tx.SetState(ctx, State{LastPoll: js.State.LastPoll, LastAdded: js.State.LastAdded})
	})
	if err != nil {
		fmt.Println("import failed:", err)
		os.Exit(1)
	}
	fmt.Printf("imported %d sources / %d collections / %d items from %s\n", ns, nc, ni, path)
}

/* ------------------------------------------------------------- main */

func main() {
	if len(os.Args) > 1 && os.Args[1] == "import" {
		path := "data/store.json"
		if len(os.Args) > 2 {
			path = os.Args[2]
		}
		runImport(path)
		return
	}

	var err error
	store, err = repo.Open(env("LOUPE_DB_DRIVER", ""), env("LOUPE_DB_DSN", ""))
	if err != nil {
		fmt.Println("db open:", err)
		os.Exit(1)
	}
	defer store.Close()

	checkGdl()
	if gdlInfo.Available {
		fmt.Printf("gallery-dl %s found\n", gdlInfo.Version)
	} else {
		fmt.Println("gallery-dl NOT found — install it: pipx install gallery-dl")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/version", handleVersion)
	mux.HandleFunc("/api/stats", handleStats)
	mux.HandleFunc("/api/sources", handleSources) // GET list, POST create
	mux.HandleFunc("/api/sources/update", postOnly(handleSourceUpdate))
	mux.HandleFunc("/api/sources/rescan", postOnly(handleSourceRescan))
	mux.HandleFunc("/api/sources/purge-stale", postOnly(handleSourcePurgeStale))
	mux.HandleFunc("/api/sources/remove", postOnly(handleSourceRemove))
	mux.HandleFunc("/api/collections", handleCollections) // GET list, POST create
	mux.HandleFunc("/api/collections/remove", postOnly(handleCollectionRemove))
	mux.HandleFunc("/api/collections/member", postOnly(handleCollectionMember))
	mux.HandleFunc("/api/configs", handleConfigs) // GET list, POST create
	mux.HandleFunc("/api/configs/update", postOnly(handleConfigUpdate))
	mux.HandleFunc("/api/configs/remove", postOnly(handleConfigRemove))
	mux.HandleFunc("/api/next", handleNext)
	mux.HandleFunc("/api/vote", postOnly(handleVote))
	mux.HandleFunc("/api/unvote", postOnly(handleUnvote))
	mux.HandleFunc("/api/selects", handleSelects)
	mux.HandleFunc("/api/unselect", postOnly(handleUnselect))
	mux.HandleFunc("/api/poll", postOnly(handlePoll))
	mux.HandleFunc("/api/selects.txt", handleSelectsTxt)
	mux.HandleFunc("/api/selects.zip", handleSelectsZip)
	mux.Handle("/", staticHandler())

	go func() {
		time.Sleep(time.Second)
		pollAll()
		t := time.NewTicker(time.Duration(cfg.PollMin) * time.Minute)
		for range t.C {
			pollAll()
		}
	}()

	srv := &http.Server{
		Addr:    cfg.Addr + ":" + cfg.Port,
		Handler: guard(mux),
		// No WriteTimeout: the zip export streams for as long as the download
		// takes. Header/idle limits are what keep half-open connections bounded.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	fmt.Printf("Loupe service on http://localhost:%s · polling every %d min\n", cfg.Port, cfg.PollMin)
	if err := srv.ListenAndServe(); err != nil {
		fmt.Println("server error:", err)
		os.Exit(1)
	}
}
