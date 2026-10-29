import adapter from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';
import { sveltekit } from '@sveltejs/kit/vite';

export default {
  plugins: [
    sveltekit({
      preprocess: vitePreprocess(),
      // Build to static files; Go embeds the output. SPA fallback for client routing.
      adapter: adapter({ fallback: 'index.html', pages: 'build', assets: 'build' })
    })
  ],
  // Dev only: the SvelteKit dev server proxies /api to the Go backend.
  server: { proxy: { '/api': 'http://localhost:8787' } }
};
