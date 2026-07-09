import { api } from './api.js';

// Universal reactive state (Svelte 5 runes in a .svelte.js module).
export const S = $state({
  view: 'review',          // review | selects | collections | sources
  scope: 'all',            // all | stale | source:<id> | collection:<id> (review)
  selectScope: 'all',      // all | source:<id> | collection:<id> (selects filter)
  queue: [],
  history: [],             // recently-voted items, for undo (this session)
  selects: [],
  sources: [],
  collections: [],
  configs: [],             // named, reusable gallery-dl configs sources can share
  stats: null,
  version: null,           // build metadata for the About page
  editing: null,           // source id being edited
  editingConfig: null,     // config id being edited
  toast: ''
});

let toastTimer;
export function toast(msg) {
  S.toast = msg;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (S.toast = ''), 2400);
}

export async function refreshStats() {
  try { S.stats = await api.get('/api/stats'); } catch { S.stats = null; }
}
// Build metadata is immutable per run, so fetch it once and cache it.
export async function loadVersion() {
  if (S.version) return;
  try { S.version = await api.get('/api/version'); } catch { S.version = null; }
}
export async function loadLibrary() {
  const [s, c, cf] = await Promise.all([api.get('/api/sources'), api.get('/api/collections'), api.get('/api/configs')]);
  S.sources = s || [];
  S.collections = c || [];
  S.configs = cf || [];
}
// Deep link (/review/{id}): the item to pin to the front of the first fetch.
let pendingFirst = null;
export function setPendingFirst(id) { pendingFirst = id; }

export async function fill() {
  try {
    let url = `/api/next?scope=${encodeURIComponent(S.scope)}&count=30`;
    if (pendingFirst) {
      url += `&first=${encodeURIComponent(pendingFirst)}`;
      pendingFirst = null; // only the first fetch jumps to the deep-linked item
    }
    const more = await api.get(url);
    const have = new Set(S.queue.map((q) => q.id));
    S.queue = [...S.queue, ...more.filter((m) => !have.has(m.id))];
    preloadAhead();
  } catch { /* ignore */ }
}

// ---- sequential image preloading -------------------------------------------
// While rating, warm the browser cache for the upcoming cards so each one is
// instant when it reaches the front. Depth is configurable (LOUPE_PRELOAD_DEPTH,
// default 3) and surfaced via /api/stats. Loading is strictly SEQUENTIAL: card
// N+2's image only starts once N+1's has finished, and so on. A single-flight
// guard means a vote/undo mid-load doesn't spawn a parallel chain — the running
// walk re-reads the (now shifted) queue by position and keeps moving forward.
const preloaded = new Set(); // media URLs already fetched this review session
let preloading = false;
let preloadAgain = false;    // a queue change arrived mid-walk → walk once more after

// The image the rating card actually shows: the full url for gifs (so they
// animate) or a video's poster, otherwise the lighter sample. Mirrors SwipeCard.
function previewSrc(item) {
  const ext = (item.url || '').split(/[?#]/)[0].split('.').pop().toLowerCase();
  return ext === 'gif' ? item.url : (item.sample || item.url);
}
function loadImage(src) {
  return new Promise((resolve) => {
    const img = new Image();
    img.onload = img.onerror = () => resolve();
    img.src = src;
  });
}
export async function preloadAhead() {
  if (preloading) { preloadAgain = true; return; } // coalesce; re-walk once we're idle
  preloading = true;
  try {
    do {
      preloadAgain = false;
      const depth = S.stats?.preloadDepth ?? 3;
      for (let i = 1; i <= depth; i++) {
        const it = S.queue[i]; // index, not identity: a vote shifts the window forward
        if (!it) break;        // queue shorter than depth — nothing more to warm yet
        const src = previewSrc(it);
        if (!src || preloaded.has(src)) continue;
        await loadImage(src);  // wait for this one before starting the next
        preloaded.add(src);
      }
    } while (preloadAgain); // a vote/undo landed mid-walk → cover the shifted window
  } finally {
    preloading = false;
  }
}
export async function startReview() {
  S.queue = [];
  S.history = [];
  preloaded.clear(); // new scope/queue → drop the old warm set (bounds memory)
  await fill();
}
export function setScope(v) {
  S.scope = v;
  startReview();
}
// Optimistically mirror a decision into the cached per-source and
// per-collection counts, so the scope chips count down while reviewing without
// refetching the whole library on every swipe. The next loadLibrary() re-syncs
// from the server. `revert` undoes the same adjustment (for undo).
function applyDecisionToCounts(item, decision, revert = false) {
  const d = revert ? -1 : 1;
  const from = item.gone ? 'staleNew' : 'new'; // stale items count separately
  const to = decision === 'good' ? 'good' : 'bad';
  const adj = (c) => {
    if (!c) return;
    c[from] = Math.max(0, (c[from] || 0) - d);
    c[to] = Math.max(0, (c[to] || 0) + d);
  };
  adj(S.sources.find((s) => s.id === item.sourceId)?.counts);
  for (const col of S.collections) {
    if (col.sourceIds?.includes(item.sourceId)) adj(col.counts);
  }
}
export function vote(decision) {
  const it = S.queue[0];
  if (!it) return;
  S.queue = S.queue.slice(1);
  // remember the decision so undo can reverse the optimistic count change
  S.history = [...S.history, { ...it, _decision: decision }].slice(-50);
  applyDecisionToCounts(it, decision);
  api.post('/api/vote', { id: it.id, decision }).then(refreshStats);
  if (S.queue.length < 5) fill();
  preloadAhead(); // window moved forward → warm the newly-revealed card(s)
}
export function undo() {
  const last = S.history[S.history.length - 1];
  if (!last) return;
  S.history = S.history.slice(0, -1);
  S.queue = [last, ...S.queue.filter((q) => q.id !== last.id)];
  if (last._decision) applyDecisionToCounts(last, last._decision, true);
  api.post('/api/unvote', { id: last.id }).then(refreshStats);
  preloadAhead();
}
export async function checkNow() {
  const r = await api.post('/api/poll', {});
  toast(r.added ? `+${r.added} new` : 'Nothing new');
  await loadLibrary();
  await fill();
  refreshStats();
}

// selects
export async function loadSelects() {
  S.selects = (await api.get('/api/selects?scope=' + encodeURIComponent(S.selectScope))) || [];
}
// Change the selects filter (source/collection/all) and reload the kept grid.
export function setSelectScope(v) {
  S.selectScope = v;
  loadSelects();
}
export async function unselect(id) {
  await api.post('/api/unselect', { id });
  loadSelects();
  refreshStats();
  loadLibrary();
}

// sources
export async function addSource(name, url, description, configFile, configJson, configId) {
  const r = await api.post('/api/sources', { name, url, description, configFile, configJson, configId });
  if (r.error) { toast(r.error); return false; }
  toast('Watching — scanning the full gallery…');
  setTimeout(() => { loadLibrary(); refreshStats(); }, 1500);
  return true;
}
export async function saveSource(id, name, url, description, configFile, configJson, configId) {
  const r = await api.post('/api/sources/update', { id, name, url, description, configFile, configJson, configId });
  if (r.error) { toast(r.error); return false; }
  toast(r.urlChanged ? 'URL changed — old items marked stale (kept, not deleted).' : 'Saved');
  S.editing = null;
  await loadLibrary();
  refreshStats();
  return true;
}
export async function rescanSource(id) {
  const r = await api.post('/api/sources/rescan', { id });
  if (r.error) { toast(r.error); return; }
  if (r.lastError) toast(r.lastError);
  else toast(r.added ? `+${r.added} new` : 'No new items');
  await loadLibrary();
  if (S.scope === 'all' || S.scope === 'source:' + id) await startReview();
  refreshStats();
}
export async function removeSource(id) {
  await api.post('/api/sources/remove', { id });
  loadLibrary();
  refreshStats();
}
export async function purgeStale(id) {
  const r = await api.post('/api/sources/purge-stale', { id });
  toast(`Cleared ${r.purged} stale`);
  loadLibrary();
  refreshStats();
}

// collections
export async function createCollection(name, description) {
  const r = await api.post('/api/collections', { name, description });
  if (r.error) { toast(r.error); return false; }
  loadLibrary();
  refreshStats();
  return true;
}
export async function removeCollection(id) {
  await api.post('/api/collections/remove', { id });
  loadLibrary();
  refreshStats();
}
export async function member(collectionId, sourceId, action) {
  await api.post('/api/collections/member', { collectionId, sourceId, action });
  loadLibrary();
}

// configs (named, reusable gallery-dl configs shared across sources)
export async function createConfig(name, configJson) {
  const r = await api.post('/api/configs', { name, configJson });
  if (r.error) { toast(r.error); return false; }
  loadLibrary();
  return true;
}
export async function saveConfig(id, name, configJson) {
  const r = await api.post('/api/configs/update', { id, name, configJson });
  if (r.error) { toast(r.error); return false; }
  toast('Saved — applies on the next poll');
  S.editingConfig = null;
  loadLibrary();
  return true;
}
export async function removeConfig(id) {
  await api.post('/api/configs/remove', { id });
  loadLibrary();
}

export async function init() {
  await loadLibrary();
  await startReview();
  refreshStats();
  setInterval(refreshStats, 60000);
}
