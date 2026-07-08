import { sveltekit } from '@sveltejs/kit/vite';

export default {
  plugins: [sveltekit()],
  // Dev only: the SvelteKit dev server proxies /api to the Go backend.
  server: { proxy: { '/api': 'http://localhost:8787' } }
};
