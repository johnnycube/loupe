<script>
  import { S } from '$lib/state.svelte.js';

  // Reusable scope filter bar, shared by Review and Selects. Renders All /
  // (optional) Stale / collection / source chips, plus a quick-filter input that
  // narrows the collection+source chips by name — handy once there are many.
  // `countKey` picks which per-scope tally to show on each chip ('new' while
  // reviewing the backlog, 'good' for kept selects).
  let { scope, onpick, countKey = 'new', showStale = false, staleN = 0 } = $props();

  let q = $state('');
  const needle = $derived(q.trim().toLowerCase());
  const match = (name) => (name || '').toLowerCase().includes(needle);
  const cols = $derived(needle ? S.collections.filter((c) => match(c.name)) : S.collections);
  const srcs = $derived(needle ? S.sources.filter((s) => match(s.name)) : S.sources);
</script>

<div class="scopebar">
  <button class="scopechip" class:active={scope === 'all'} onclick={() => onpick('all')}>All</button>
  {#if showStale && staleN}
    <button class="scopechip" class:active={scope === 'stale'} onclick={() => onpick('stale')}>⚑ Stale <span class="n">{staleN}</span></button>
  {/if}
  <input class="scopefilter" placeholder="filter…" bind:value={q} aria-label="Filter sources and collections" />
  {#each cols as c (c.id)}
    <button class="scopechip" class:active={scope === 'collection:' + c.id} onclick={() => onpick('collection:' + c.id)}>▤ {c.name} <span class="n">{c.counts[countKey]}</span></button>
  {/each}
  {#each srcs as s (s.id)}
    <button class="scopechip" class:active={scope === 'source:' + s.id} onclick={() => onpick('source:' + s.id)}>{s.name} <span class="n">{s.counts[countKey]}</span></button>
  {/each}
  {#if needle && !cols.length && !srcs.length}<span class="scopenone">no matches</span>{/if}
</div>
