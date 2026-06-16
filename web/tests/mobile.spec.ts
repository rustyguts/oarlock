import { test, expect, type Page } from '@playwright/test';
import { mockApi, WF_ID, RUN_ID } from './mock-api';

// Small-phone visual regression (iPhone SE class, 375×667 — below the 768px
// mobile breakpoint, so the nav collapses to a sheet). Same deterministic setup
// as the desktop suite (ui.spec.ts); baselines live alongside it as
// `mobile-*-linux.png`. Regenerate with `make screenshots`.

const FROZEN_NOW = new Date('2026-06-12T09:05:00Z');

test.use({
	baseURL: 'http://localhost:4173',
	locale: 'en-US',
	timezoneId: 'UTC',
	viewport: { width: 375, height: 667 }
});

test.beforeEach(async ({ page }) => {
	await mockApi(page);
	await page.clock.setFixedTime(FROZEN_NOW); // stable "Xm ago" strings
});

async function settle(page: Page) {
	await page.waitForLoadState('networkidle');
	await page.waitForTimeout(250); // let fitView/layout finish
}

test('mobile — dashboard', async ({ page }) => {
	await page.goto('/');
	await page.waitForSelector('text=Total runs');
	await page.waitForSelector('text=Task outcomes');
	await settle(page);
	await expect(page).toHaveScreenshot('mobile-dashboard.png');
});

test('mobile — dashboard, dark', async ({ page }) => {
	await page.emulateMedia({ colorScheme: 'dark' });
	await page.goto('/');
	await page.waitForSelector('text=Total runs');
	await settle(page);
	await expect(page).toHaveScreenshot('mobile-dashboard-dark.png');
});

test('mobile — nav sheet open', async ({ page }) => {
	await page.goto('/');
	await page.waitForSelector('text=Total runs');
	await page.click('[data-sidebar="trigger"]');
	await page.waitForSelector('[data-mobile="true"] >> text=Connections');
	await settle(page);
	await expect(page).toHaveScreenshot('mobile-nav.png');
});

test('mobile — workflow list', async ({ page }) => {
	await page.goto('/workflows');
	await page.waitForSelector('text=Order Sync');
	await settle(page);
	await expect(page).toHaveScreenshot('mobile-workflow-list.png');
});

test('mobile — connections', async ({ page }) => {
	await page.goto('/mcp');
	await page.waitForSelector('text=github-tools');
	await settle(page);
	await expect(page).toHaveScreenshot('mobile-connections.png');
});

test('mobile — configuration', async ({ page }) => {
	await page.goto('/configuration');
	await page.waitForSelector('text=my_anthropic');
	await settle(page);
	await expect(page).toHaveScreenshot('mobile-configuration.png');
});

test('mobile — run history', async ({ page }) => {
	await page.goto(`/workflows/${WF_ID}/runs`);
	await page.waitForSelector('text=Total runs');
	await page.waitForSelector('a[href^="/runs/"]');
	await settle(page);
	await expect(page).toHaveScreenshot('mobile-run-history.png');
});

test('mobile — run detail', async ({ page }) => {
	await page.goto(`/runs/${RUN_ID}`);
	await page.waitForFunction(() => document.querySelectorAll('.svelte-flow__node').length === 4);
	await settle(page);
	await expect(page).toHaveScreenshot('mobile-run-detail.png');
});

test('mobile — editor', async ({ page }) => {
	await page.goto(`/workflows/${WF_ID}`);
	await page.waitForFunction(() => document.querySelectorAll('.svelte-flow__node').length === 4);
	await settle(page);
	await expect(page).toHaveScreenshot('mobile-editor.png');
});
