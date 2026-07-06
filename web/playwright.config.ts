import { defineConfig } from '@playwright/test';

// Visual regression tests. The API is fully mocked (tests/mock-api.ts) and
// the clock frozen, so snapshots are deterministic and need no backend.
// Baselines are platform-specific (committed for darwin); regenerate with
// `bun run test:ui:update` after intentional UI changes.
export default defineConfig({
	testDir: './tests',
	fullyParallel: true,
	snapshotPathTemplate: '{testDir}/__snapshots__/{arg}-{platform}{ext}',
	use: {
		viewport: { width: 1440, height: 900 },
		deviceScaleFactor: 1
	},
	expect: {
		toHaveScreenshot: {
			maxDiffPixels: 50, // absorb antialiasing noise only — a button-sized change must fail
			animations: 'disabled',
			caret: 'hide'
		}
	},
	webServer: {
		command: 'bun run build && bun run preview --port 4173 --strictPort',
		port: 4173,
		reuseExistingServer: !process.env.CI,
		timeout: 180_000
	}
});
