import adapter from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

export default {
  preprocess: vitePreprocess(),
  kit: {
    // Build to static files; Go embeds the output. SPA fallback for client routing.
    adapter: adapter({ fallback: 'index.html', pages: 'build', assets: 'build' })
  }
};
