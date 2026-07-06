import adapter from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

/** @type {import('@sveltejs/kit').Config} */
export default {
	preprocess: vitePreprocess(),
	kit: {
		// Static SPA build: the app is client-rendered (ssr disabled in the root
		// layout) and served by the Go API binary, which embeds `build/` and
		// serves the fallback page for any non-API route.
		adapter: adapter({ fallback: 'index.html' })
	}
};
