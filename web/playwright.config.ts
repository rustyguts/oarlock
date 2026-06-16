import { defineConfig } from '@playwright/test';

// Visual regression tests. The API is fully mocked (tests/mock-api.ts) and
// the clock frozen, so snapshots are deterministic and need no backend.
// Baselines are Linux-only and rendered inside a pinned Playwright Docker
// image so they reproduce identically on any host OS. Do NOT regenerate them
// with a bare `bun run test:ui:update` (host fonts/emoji differ from the
// container and will drift) — run `make screenshots` instead (works on macOS
// too; see README "Visual regression").
export default defineConfig({
	testDir: './tests',
	fullyParallel: true,
	// Linux-only baselines: the suffix is hard-coded (not {platform}) so every
	// host compares against the same committed set. Regenerate via `make screenshots`.
	snapshotPathTemplate: '{testDir}/__snapshots__/{arg}-linux{ext}',
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
		command: 'bun run build && bun run preview -- --port 4173 --strictPort',
		port: 4173,
		reuseExistingServer: !process.env.CI,
		timeout: 180_000
	}
});
