// ESLint flat config for the SvelteKit UI (plain JavaScript + Svelte 5 runes).
import js from '@eslint/js';
import svelte from 'eslint-plugin-svelte';
import prettier from 'eslint-config-prettier';
import globals from 'globals';
import { loadConfig } from '@sveltejs/load-config';

const svelteConfig = (await loadConfig('./', { traverse: false }))?.config;

export default [
  { ignores: ['build/', '.svelte-kit/', 'node_modules/'] },
  js.configs.recommended,
  ...svelte.configs.recommended,
  // Prettier owns formatting; drop every stylistic rule that would fight it.
  prettier,
  ...svelte.configs.prettier,
  {
    languageOptions: { globals: { ...globals.browser, ...globals.node } }
  },
  {
    files: ['**/*.svelte', '**/*.svelte.js'],
    languageOptions: { parserOptions: { svelteConfig } }
  },
  {
    rules: {
      // No debugging output left behind; errors and warnings are deliberate.
      'no-console': ['error', { allow: ['error', 'warn'] }],
      'no-debugger': 'error',
      // Loupe is one catch-all route served at the domain root: hrefs point at
      // /api downloads and external item URLs, and replaceState mirrors the
      // in-page view. resolve() would add nothing here.
      'svelte/no-navigation-without-resolve': 'off'
    }
  }
];
