<script>
  let { item, onvote, onzoom } = $props();

  // Some booru "images" are actually videos (mp4/webm/…) or animated gifs.
  // Pick the right element: <video> for video, the full url for gifs (so they
  // animate), otherwise the lighter sample image.
  const VIDEO_EXT = ['mp4', 'webm', 'm4v', 'mov', 'ogv', 'ogg'];
  function extOf(u) { return (u || '').split(/[?#]/)[0].split('.').pop().toLowerCase(); }
  let kind = $derived(extOf(item.url));
  let isVideo = $derived(VIDEO_EXT.includes(kind));
  let mediaSrc = $derived(kind === 'gif' ? item.url : item.sample);

  let el;
  let dx = $state(0);
  let exiting = $state(0); // -1 left (pass), 1 right (keep)
  let dragging = false;
  let startX = 0;

  function down(e) {
    dragging = true;
    startX = e.clientX;
    el.setPointerCapture(e.pointerId);
  }
  function move(e) {
    if (!dragging) return;
    dx = e.clientX - startX;
  }
  function up() {
    if (!dragging) return;
    dragging = false;
    const moved = Math.abs(dx);
    if (moved > 100) { fly(dx > 0 ? 'good' : 'bad'); return; }
    if (moved < 6) onzoom?.(); // a tap (not a drag) → open the full-screen view
    dx = 0;
  }

  // Called by drag release and by the parent's Pass/Keep buttons.
  export function fly(decision) {
    if (exiting !== 0) return;
    exiting = decision === 'good' ? 1 : -1;
    setTimeout(() => onvote(decision), 230);
  }

  let style = $derived(
    exiting !== 0
      ? `transform:translate(${exiting * window.innerWidth}px,40px) rotate(${exiting * 20}deg);opacity:0;transition:transform .3s,opacity .3s;`
      : dragging
        ? `transform:translate(${dx}px,0) rotate(${dx / 18}deg);`
        : `transform:translate(${dx}px,0) rotate(${dx / 18}deg);transition:transform .25s;`
  );
  let keepOp = $derived(Math.max(0, Math.min(dx / 100, 1)));
  let passOp = $derived(Math.max(0, Math.min(-dx / 100, 1)));
</script>

<div
  class="card"
  bind:this={el}
  style={style}
  onpointerdown={down}
  onpointermove={move}
  onpointerup={up}
  onpointercancel={up}
>
  <div class="mat">
    {#if isVideo}
      <video
        src={item.url}
        poster={item.sample}
        autoplay
        loop
        muted
        playsinline
        draggable="false"
      ></video>
    {:else}
      <img
        src={mediaSrc}
        alt=""
        draggable="false"
        onerror={(e) => { e.currentTarget.onerror = null; e.currentTarget.src = item.url; }}
      />
    {/if}
  </div>
  <div class="cap"><span class="src">{item.label || ''}</span><span>{item.title || ''}</span></div>
  <a
    class="origlink"
    href={item.url}
    target="_blank"
    rel="noopener"
    title="Open original image"
    onpointerdown={(e) => e.stopPropagation()}
    onclick={(e) => e.stopPropagation()}
  >↗ original</a>
  {#if item.gone}<div class="cardtag">stale source</div>{/if}
  <div class="stamp keep" style="opacity:{keepOp}">KEEP</div>
  <div class="stamp pass" style="opacity:{passOp}">PASS</div>
</div>
