import { defineConfig } from 'vitest/config';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// Minimal unit-test runner for pure library code (flow.ts and friends). The
// svelte plugin is only here so `.svelte.ts` rune modules (e.g. clock.svelte.ts,
// pulled in transitively) compile; no jsdom — these tests touch no DOM.
export default defineConfig({
	plugins: [svelte()],
	test: {
		environment: 'node',
		include: ['src/**/*.test.ts']
	}
});
