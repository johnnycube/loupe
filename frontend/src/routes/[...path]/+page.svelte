<script>
  import { onMount } from 'svelte';
  import { SvelteSet } from 'svelte/reactivity';
  import { replaceState } from '$app/navigation';
  import SwipeCard from '$lib/SwipeCard.svelte';
  import ScopeBar from '$lib/ScopeBar.svelte';
  import {
    S, init, checkNow, vote, undo, setScope, loadSelects, setSelectScope, unselect,
    addSource, saveSource, removeSource, purgeStale, rescanSource,
    createCollection, removeCollection, member, setPendingFirst, loadVersion,
    createConfig, saveConfig, removeConfig
  } from '$lib/state.svelte.js';

  let card = $state(null);
  let checking = $state(false);
  let rescanning = new SvelteSet(); // source ids currently rescanning (parallel-safe)
  let zoom = $state(false); // full-screen view of the current card

  let addName = $state(''), addUrl = $state(''), addDesc = $state(''), addCfg = $state(''), addCfgJson = $state(''), addCfgId = $state('');
  let editName = $state(''), editUrl = $state(''), editDesc = $state(''), editCfg = $state(''), editCfgJson = $state(''), editCfgId = $state('');
  let colName = $state(''), colDesc = $state('');
  let memberSel = $state({});
  // shared-config (Configs view) form state
  let cfgName = $state(''), cfgJson = $state('');
  let editCfgName = $state(''), editCfgJsonBody = $state('');

  const current = $derived(S.queue[0]);
  const next = $derived(S.queue[1]);
  const counts = $derived(S.stats?.counts ?? { new: 0, good: 0, bad: 0, gone: 0, staleNew: 0 });
  const staleN = $derived(counts.staleNew || 0);
  const gdlMissing = $derived(S.stats?.gdl && S.stats.gdl.available === false);

  function go(v) { S.view = v; if (v === 'selects') loadSelects(); if (v === 'about') loadVersion(); }
  // Jump from the sources list straight into reviewing just that source.
  function reviewSource(id) { go('review'); pick('source:' + id); }
  // Jump from a green keep-count straight into Selects, pre-filtered to that
  // source/collection. Set the scope before switching so go()'s load is scoped.
  function selectsScoped(scope) { S.selectScope = scope; go('selects'); }
  async function refresh() { checking = true; await checkNow(); checking = false; }
  function pick(v) { setScope(v); }

  function startEdit(w) { S.editing = w.id; editName = w.name; editUrl = w.url; editDesc = w.description || ''; editCfg = w.configFile || ''; editCfgJson = w.configJson || ''; editCfgId = w.configId || ''; }
  async function submitEdit(id) { await saveSource(id, editName.trim(), editUrl.trim(), editDesc, editCfg.trim(), editCfgJson.trim(), editCfgId); }
  async function submitAdd() {
    if (!addUrl.trim()) return;
    if (await addSource(addName.trim(), addUrl.trim(), addDesc.trim(), addCfg.trim(), addCfgJson.trim(), addCfgId)) {
      addName = ''; addUrl = ''; addDesc = ''; addCfg = ''; addCfgJson = ''; addCfgId = '';
    }
  }
  // shared configs (Configs view)
  function startEditConfig(c) { S.editingConfig = c.id; editCfgName = c.name; editCfgJsonBody = c.configJson || ''; }
  async function submitConfig() {
    if (!cfgName.trim()) return;
    if (await createConfig(cfgName.trim(), cfgJson.trim())) { cfgName = ''; cfgJson = ''; }
  }
  async function submitEditConfig(id) { await saveConfig(id, editCfgName.trim(), editCfgJsonBody.trim()); }
  function configName(id) { return S.configs.find((c) => c.id === id)?.name; }
  async function rescan(id) { rescanning.add(id); try { await rescanSource(id); } finally { rescanning.delete(id); } }
  function fmtBuildTime(s) {
    if (!s) return 'unknown';
    const d = new Date(s);
    return isNaN(d) ? s : d.toLocaleString();
  }
  async function submitCollection() {
    if (!colName.trim()) return;
    if (await createCollection(colName.trim(), colDesc.trim())) { colName = ''; colDesc = ''; }
  }
  function avail(c) {
    const ids = new Set((c.members || []).map((m) => m.id));
    return S.sources.filter((s) => !ids.has(s.id));
  }
  function isVid(u) {
    return ['mp4', 'webm', 'm4v', 'mov', 'ogv', 'ogg'].includes((u || '').split(/[?#]/)[0].split('.').pop().toLowerCase());
  }
  // In the deck, a decision plays the swipe-fly animation; in the full-screen
  // view the card is hidden, so vote immediately and let the lightbox advance to
  // the next item.
  function decide(d) { if (zoom) vote(d); else card?.fly(d); }
  function onKey(e) {
    if (zoom && e.key === 'Escape') { e.preventDefault(); zoom = false; return; }
    if (S.view !== 'review' || !S.queue.length) return;
    if (e.key === 'ArrowRight') { e.preventDefault(); decide('good'); }
    else if (e.key === 'ArrowLeft') { e.preventDefault(); decide('bad'); }
    else if (e.key === 'ArrowUp' || e.key === 'Backspace') { e.preventDefault(); undo(); }
  }

  // Close the full-screen view when it has nothing to show (queue emptied, or the
  // user navigated to another tab).
  $effect(() => { if (zoom && (S.view !== 'review' || !current)) zoom = false; });

  // ---- routing: keep the address bar + tab title in sync with the view, and
  // use /review/{uuid} for the current card while tindering. replaceState keeps
  // SvelteKit's own history.state intact and never triggers a navigation. ----
  const TITLES = { review: 'Review', selects: 'Selects', collections: 'Collections', sources: 'Sources', about: 'About' };
  function viewFromPath(p) {
    const seg = (p || '/').split('/').filter(Boolean)[0];
    return TITLES[seg] ? seg : 'review';
  }
  function pathFor(view, item) {
    if (view === 'review') return item ? `/review/${item.id}` : '/review';
    return '/' + view;
  }
  // adopt the initial view from the URL before the first effect runs; for
  // /review/{id}, pin that item to the front of the first review fetch.
  if (typeof window !== 'undefined') {
    const seg = window.location.pathname.split('/').filter(Boolean);
    S.view = viewFromPath(window.location.pathname);
    if (seg[0] === 'review' && seg[1]) setPendingFirst(seg[1]);
  }

  $effect(() => {
    if (typeof document === 'undefined') return;
    const item = S.view === 'review' ? S.queue[0] : null;
    document.title = (TITLES[S.view] || 'Loupe') + ' · Loupe';
    const path = pathFor(S.view, item);
    if (window.location.pathname !== path) {
      try { replaceState(path, {}); } catch { /* router not ready yet; next run will sync */ }
    }
  });

  onMount(() => {
    init().then(() => { if (S.view === 'selects') loadSelects(); if (S.view === 'about') loadVersion(); });
    const onpop = () => {
      const v = viewFromPath(window.location.pathname);
      if (v !== S.view) go(v);
    };
    window.addEventListener('popstate', onpop);
    return () => window.removeEventListener('popstate', onpop);
  });
</script>

<svelte:window onkeydown={onKey} />

<div class="app">
  {#if gdlMissing}
    <div class="banner">gallery-dl isn’t installed. Install it: <code>pipx install gallery-dl</code>, then restart.</div>
  {/if}

  <header class="topbar">
    <div><span class="mark">Loupe<span class="dot">.</span></span><span class="headcount"><b>{counts.new}</b> to review</span></div>
    <div class="topactions">
      <button class="iconbtn {checking ? 'spin' : ''}" onclick={refresh} aria-label="Check for new images">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 12a9 9 0 1 1-2.64-6.36M21 4v5h-5"/></svg>
      </button>
      <button class="iconbtn" class:active={S.view === 'about'} onclick={() => go('about')} aria-label="About Loupe">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="9"/><path d="M12 11v5M12 7.5v.5"/></svg>
      </button>
    </div>
  </header>

  {#if S.view === 'review'}
    <section class="review">
      <ScopeBar scope={S.scope} onpick={pick} countKey="new" showStale staleN={staleN} />

      {#if current}
        <div class="deck">
          {#if next}
            <div class="card behind"><div class="mat"><img src={next.sample} alt="" /></div></div>
          {/if}
          {#key current.id}
            <SwipeCard bind:this={card} item={current} onvote={vote} onzoom={() => (zoom = true)} />
          {/key}
        </div>
        <div class="actions">
          <button class="act mid undo" aria-label="Undo" disabled={!S.history.length} onclick={undo}>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M9 14L4 9l5-5"/><path d="M4 9h11a5 5 0 0 1 0 10h-4"/></svg>
          </button>
          <button class="act big pass" aria-label="Pass" onclick={() => card?.fly('bad')}>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2"><path d="M6 6l12 12M18 6L6 18"/></svg>
          </button>
          <button class="act big keep" aria-label="Keep" onclick={() => card?.fly('good')}>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 21s-7.5-4.6-10-9.3C.4 8.4 2 5 5.2 5 7.3 5 8.7 6.2 12 9c3.3-2.8 4.7-4 6.8-4C22 5 23.6 8.4 22 11.7 19.5 16.4 12 21 12 21z"/></svg>
          </button>
        </div>
        <div class="hint">swipe or tap · <span class="key">←</span> pass <span class="key">→</span> keep <span class="key">↑</span> undo</div>
      {:else}
        <div class="pad">
          <h2>{S.scope === 'stale' ? 'No stale items' : 'All caught up'}</h2>
          <p>{S.scope === 'stale' ? 'Nothing orphaned left to review.' : 'Nothing new here. Try another scope above, add sources, or tap refresh.'}</p>
          <button class="btn primary" onclick={() => go('sources')}>Add sources</button>
        </div>
      {/if}
    </section>
  {/if}

  {#if S.view === 'selects'}
    <main><div class="page">
      <div class="sheethead"><div><h2>Selects</h2><div class="sub">{S.selects.length ? S.selects.length + ' kept' : 'nothing kept yet'}</div></div></div>
      <ScopeBar scope={S.selectScope} onpick={setSelectScope} countKey="good" />
      {#if S.selects.length}
        <div class="exportbar"><a class="btn" href="/api/selects.txt?scope={encodeURIComponent(S.selectScope)}" download>URL list</a><a class="btn primary" href="/api/selects.zip?scope={encodeURIComponent(S.selectScope)}">Download ZIP</a></div>
        <div class="sheet" style="margin-top:14px">
          {#each S.selects as g (g.id)}
            <div class="cell" class:stale={g.gone}>
              <button class="rm" aria-label="Remove" onclick={() => unselect(g.id)}><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6"><path d="M6 6l12 12M18 6L6 18"/></svg></button>
              {#if isVid(g.url)}<span class="vidbadge">▶ video</span>{/if}
              {#if g.gone}<span class="stale-tag">stale</span>{/if}
              <a href={g.url} target="_blank" rel="noopener"><div class="mat"><img src={g.sample} loading="lazy" alt="" /></div></a>
              <span class="num">{g.label || ''}</span>
            </div>
          {/each}
        </div>
      {:else}
        <div class="pad"><p>{S.selectScope === 'all' ? 'Keepers show up here as you swipe.' : 'Nothing kept in this filter yet.'}</p></div>
      {/if}
    </div></main>
  {/if}

  {#if S.view === 'collections'}
    <main><div class="page">
      <div class="sheethead"><div><h2>Collections</h2><div class="sub">group sources to review together</div></div></div>
      <div class="form">
        <input placeholder="collection name" bind:value={colName} />
        <div class="twocol"><input placeholder="description (optional)" bind:value={colDesc} /><button class="btn primary" onclick={submitCollection}>Create</button></div>
      </div>
      <div class="wlist">
        {#each S.collections as c (c.id)}
          <div class="witem">
            <div class="top">
              <div class="info"><div class="lbl">{c.name}</div>{#if c.description}<div class="desc">{c.description}</div>{/if}</div>
              <div class="badges"><span class="badge n">{c.counts.new}</span><button class="badge g asbtn" title="View kept in selects" onclick={() => selectsScoped('collection:' + c.id)}>{c.counts.good}</button>{#if c.counts.gone}<span class="badge stale">{c.counts.gone}</span>{/if}</div>
            </div>
            <div class="chips">
              {#if c.members && c.members.length}
                {#each c.members as m (m.id)}<span class="chip">{m.name}<button aria-label="remove" onclick={() => member(c.id, m.id, 'remove')}>×</button></span>{/each}
              {:else}<span class="sub">no sources yet</span>{/if}
            </div>
            <div class="memberadd">
              <select bind:value={memberSel[c.id]}>{#each avail(c) as s (s.id)}<option value={s.id}>{s.name}</option>{/each}</select>
              <button class="btn" onclick={() => member(c.id, memberSel[c.id] ?? avail(c)[0]?.id, 'add')} disabled={!avail(c).length}>Add</button>
            </div>
            <div class="rowactions"><button class="btn ghost" onclick={() => removeCollection(c.id)}>Delete collection</button></div>
          </div>
        {/each}
        {#if !S.collections.length}<div class="pad"><p>No collections yet. Create one above, then add sources.</p></div>{/if}
      </div>
    </div></main>
  {/if}

  {#if S.view === 'sources'}
    <main><div class="page">
      <div class="sheethead"><div><h2>Sources</h2><div class="sub">checked every {S.stats?.pollMinutes ?? 15} min</div></div></div>
      <div class="form">
        <input placeholder="name" bind:value={addName} />
        <input class="mono" placeholder="gallery URL" bind:value={addUrl} />
        <input class="mono" placeholder="gallery-dl config file (optional) · e.g. ~/.config/gallery-dl/booru.conf" bind:value={addCfg} />
        <textarea class="mono cfgbox" rows="3" placeholder="inline gallery-dl config (optional, JSON) · e.g. &#123;&quot;extractor&quot;:&#123;&quot;rule34&quot;:&#123;&quot;api-key&quot;:&quot;…&quot;,&quot;user-id&quot;:&quot;…&quot;&#125;&#125;&#125;" bind:value={addCfgJson}></textarea>
        {#if S.configs.length}
          <label class="cfgpick">shared config
            <select bind:value={addCfgId}><option value="">none</option>{#each S.configs as c (c.id)}<option value={c.id}>{c.name}</option>{/each}</select>
          </label>
        {/if}
        <div class="twocol"><input placeholder="description (optional)" bind:value={addDesc} /><button class="btn primary" onclick={submitAdd}>Add</button></div>
      </div>
      <div class="wlist">
        {#each S.sources as w (w.id)}
          {#if S.editing === w.id}
            <div class="witem"><div class="form" style="margin:0">
              <input bind:value={editName} placeholder="name" />
              <input class="mono" bind:value={editUrl} placeholder="url" />
              <input class="mono" bind:value={editCfg} placeholder="gallery-dl config file (optional) · ~/.config/gallery-dl/booru.conf" />
              <textarea class="mono cfgbox" rows="3" bind:value={editCfgJson} placeholder="inline gallery-dl config (optional, JSON)"></textarea>
              {#if S.configs.length}
                <label class="cfgpick">shared config
                  <select bind:value={editCfgId}><option value="">none</option>{#each S.configs as c (c.id)}<option value={c.id}>{c.name}</option>{/each}</select>
                </label>
              {/if}
              <input bind:value={editDesc} placeholder="description (optional)" />
              <div class="twocol"><button class="btn ghost" onclick={() => (S.editing = null)}>Cancel</button><button class="btn primary" onclick={() => submitEdit(w.id)}>Save</button></div>
            </div></div>
          {:else}
            <div class="witem">
              <div class="top">
                <div class="info">
                  <button class="lbl aslink" onclick={() => reviewSource(w.id)} title="Review this source">{w.name}</button>
                  {#if w.description}<div class="desc">{w.description}</div>{/if}
                  <div class="url">{w.url}</div>
                  {#if w.configId}<div class="cfg">⚙ shared: {configName(w.configId) ?? 'missing config'}</div>{/if}
                  {#if w.configFile}<div class="cfg">⚙ {w.configFile}</div>{/if}
                  {#if w.configJson}<div class="cfg">⚙ inline config</div>{/if}
                  {#if w.lastError}<div class="err">{w.lastError}</div>{/if}
                  {#if w.collections && w.collections.length}<div class="colchips">{#each w.collections as c}<span class="colchip">{c.name}</span>{/each}</div>{/if}
                </div>
                <div class="badges"><span class="badge n">{w.counts.new}</span><button class="badge g asbtn" title="View kept in selects" onclick={() => selectsScoped('source:' + w.id)}>{w.counts.good}</button>{#if w.counts.gone}<span class="badge stale">{w.counts.gone}</span>{/if}</div>
              </div>
              <div class="rowactions">
                <button class="btn ghost" onclick={() => rescan(w.id)} disabled={rescanning.has(w.id)}>{rescanning.has(w.id) ? 'Rescanning…' : 'Rescan'}</button>
                {#if w.counts.gone}<button class="btn ghost" onclick={() => purgeStale(w.id)}>Clear stale</button>{/if}
                <button class="btn ghost" onclick={() => startEdit(w)}>Edit</button>
                <button class="btn ghost" onclick={() => removeSource(w.id)}>Remove</button>
              </div>
            </div>
          {/if}
        {/each}
        {#if !S.sources.length}<div class="pad"><p>No sources yet. Add a gallery URL above — a booru search, an artist page, an album.</p></div>{/if}
      </div>

      <section class="subcard">
        <div class="sheethead"><div><h2>Shared configs</h2><div class="sub">reusable gallery-dl config · edit once, every source using it updates</div></div></div>
        <div class="form">
          <input placeholder="config name · e.g. reddit" bind:value={cfgName} />
          <textarea class="mono cfgbox" rows="4" placeholder="gallery-dl config (JSON) · e.g. &#123;&quot;extractor&quot;:&#123;&quot;reddit&quot;:&#123;&quot;cookies&quot;:&#123;…&#125;&#125;&#125;&#125;" bind:value={cfgJson}></textarea>
          <div class="twocol"><span class="sub">pick it via the “shared config” field when adding or editing a source</span><button class="btn primary" onclick={submitConfig}>Create</button></div>
        </div>
        <div class="wlist">
          {#each S.configs as c (c.id)}
            {#if S.editingConfig === c.id}
              <div class="witem"><div class="form" style="margin:0">
                <input bind:value={editCfgName} placeholder="config name" />
                <textarea class="mono cfgbox" rows="4" bind:value={editCfgJsonBody} placeholder="gallery-dl config (JSON)"></textarea>
                <div class="twocol"><button class="btn ghost" onclick={() => (S.editingConfig = null)}>Cancel</button><button class="btn primary" onclick={() => submitEditConfig(c.id)}>Save</button></div>
              </div></div>
            {:else}
              <div class="witem">
                <div class="top">
                  <div class="info">
                    <div class="lbl">{c.name}</div>
                    <div class="desc">used by {c.uses} source{c.uses === 1 ? '' : 's'}</div>
                  </div>
                </div>
                <div class="rowactions">
                  <button class="btn ghost" onclick={() => startEditConfig(c)}>Edit</button>
                  <button class="btn ghost" onclick={() => removeConfig(c.id)}>Delete</button>
                </div>
              </div>
            {/if}
          {/each}
          {#if !S.configs.length}<div class="pad"><p>No shared configs yet. Create one above — e.g. a “reddit” config with your cookies — then pick it when adding or editing a source.</p></div>{/if}
        </div>
      </section>
    </div></main>
  {/if}

  {#if S.view === 'about'}
    <main><div class="page">
      <div class="sheethead"><div><h2>About</h2><div class="sub">a focused loupe for curating image galleries</div></div></div>
      <dl class="about">
        <dt>Version</dt>
        <dd>{S.version?.tag || 'untagged build'}</dd>
        <dt>Commit</dt>
        <dd><code>{S.version?.commit || 'unknown'}</code>{#if S.version?.dirty}<span class="dirty-tag">modified</span>{/if}</dd>
        <dt>Built</dt>
        <dd>{fmtBuildTime(S.version?.buildTime)}</dd>
        <dt>Runtime</dt>
        <dd><code>{S.version?.goVersion || 'unknown'}</code></dd>
      </dl>
    </div></main>
  {/if}

  <nav class="bottomnav">
    <button class="navbtn" class:active={S.view === 'review'} onclick={() => go('review')}>
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><rect x="4" y="3" width="13" height="17" rx="2"/><path d="M20 7v12a2 2 0 0 1-2 2H8" opacity=".5"/></svg>
      Review{#if counts.new}<span class="nbadge">{counts.new}</span>{/if}
    </button>
    <button class="navbtn" class:active={S.view === 'selects'} onclick={() => go('selects')}>
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M12 21s-7.5-4.6-10-9.3C.4 8.4 2 5 5.2 5 7.3 5 8.7 6.2 12 9c3.3-2.8 4.7-4 6.8-4C22 5 23.6 8.4 22 11.7 19.5 16.4 12 21 12 21z"/></svg>
      Selects{#if counts.good}<span class="nbadge">{counts.good}</span>{/if}
    </button>
    <button class="navbtn" class:active={S.view === 'collections'} onclick={() => go('collections')}>
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M12 3l9 5-9 5-9-5 9-5z"/><path d="M3 13l9 5 9-5" opacity=".5"/></svg>
      Collections
    </button>
    <button class="navbtn" class:active={S.view === 'sources'} onclick={() => go('sources')}>
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><circle cx="6" cy="6" r="2"/><circle cx="6" cy="18" r="2"/><path d="M11 6h9M11 18h9M11 12h9"/></svg>
      Sources
    </button>
  </nav>

  {#if S.toast}<div class="toast">{S.toast}</div>{/if}

  {#if zoom && current}
    <div class="lightbox" role="dialog" aria-modal="true" aria-label="Full screen view">
      <!-- full-area backdrop: clicking outside the media (or its dedicated button) closes -->
      <button class="lb-backdrop" aria-label="Close full screen" onclick={() => (zoom = false)}></button>
      <button class="lb-close" aria-label="Close" onclick={() => (zoom = false)}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2"><path d="M6 6l12 12M18 6L6 18"/></svg>
      </button>
      <div class="lb-stage">
        {#key current.id}
          {#if isVid(current.url)}
            <video class="lb-media" src={current.url} poster={current.sample} autoplay loop controls playsinline>
              <track kind="captions" />
            </video>
          {:else}
            <img class="lb-media" src={current.url} alt="" onerror={(e) => { e.currentTarget.onerror = null; e.currentTarget.src = current.sample; }} />
          {/if}
        {/key}
        <a class="origlink lb-orig" href={current.url} target="_blank" rel="noopener">↗ original</a>
      </div>
      <div class="lb-bar">
        <button class="act mid undo" aria-label="Undo" disabled={!S.history.length} onclick={undo}>
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M9 14L4 9l5-5"/><path d="M4 9h11a5 5 0 0 1 0 10h-4"/></svg>
        </button>
        <button class="act big pass" aria-label="Pass" onclick={() => vote('bad')}>
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2"><path d="M6 6l12 12M18 6L6 18"/></svg>
        </button>
        <button class="act big keep" aria-label="Keep" onclick={() => vote('good')}>
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 21s-7.5-4.6-10-9.3C.4 8.4 2 5 5.2 5 7.3 5 8.7 6.2 12 9c3.3-2.8 4.7-4 6.8-4C22 5 23.6 8.4 22 11.7 19.5 16.4 12 21 12 21z"/></svg>
        </button>
      </div>
      <div class="lb-hint"><span class="key">←</span> pass <span class="key">→</span> keep <span class="key">↑</span> undo <span class="key">esc</span> close</div>
    </div>
  {/if}
</div>
