import { test, expect, type Page } from '@playwright/test';
import { mockApi, WF_ID, RUN_ID } from './mock-api';

// Visual regression suite. Any intentional UI change requires regenerating
// baselines with `npm run test:ui:update` and committing the diff — an
// unexpected snapshot failure means the UI changed when it shouldn't have.

const FROZEN_NOW = new Date('2026-06-12T09:05:00Z');

test.use({ baseURL: 'http://localhost:4173', locale: 'en-US', timezoneId: 'UTC' });

test.beforeEach(async ({ page }) => {
	await mockApi(page);
	await page.clock.setFixedTime(FROZEN_NOW); // stable "Xm ago" strings
});

async function settle(page: Page) {
	await page.waitForLoadState('networkidle');
	await page.waitForTimeout(250); // let fitView/layout finish
}

test('dashboard', async ({ page }) => {
	await page.goto('/');
	await page.waitForSelector('text=Total runs');
	await page.waitForSelector('text=Task outcomes');
	await settle(page);
	await expect(page).toHaveScreenshot('dashboard.png');
});

test('dashboard — dark', async ({ page }) => {
	await page.emulateMedia({ colorScheme: 'dark' });
	await page.goto('/');
	await page.waitForSelector('text=Total runs');
	await settle(page);
	await expect(page).toHaveScreenshot('dashboard-dark.png');
});

test('workflow list', async ({ page }) => {
	await page.goto('/workflows');
	await page.waitForSelector('text=Order Sync');
	await settle(page);
	await expect(page).toHaveScreenshot('workflow-list.png');
});

test('workflow list — dark', async ({ page }) => {
	await page.emulateMedia({ colorScheme: 'dark' });
	await page.goto('/workflows');
	await page.waitForSelector('text=Order Sync');
	await settle(page);
	await expect(page).toHaveScreenshot('workflow-list-dark.png');
});

test('editor canvas with inspector', async ({ page }) => {
	await page.goto(`/workflows/${WF_ID}`);
	await page.waitForFunction(() => document.querySelectorAll('.svelte-flow__node').length === 4);
	await page.click('.svelte-flow__node:has-text("count")');
	await page.waitForSelector('aside:has-text("Step key")');
	await settle(page);
	await expect(page).toHaveScreenshot('editor-inspector.png');
});

test('editor canvas — dark', async ({ page }) => {
	await page.emulateMedia({ colorScheme: 'dark' });
	await page.goto(`/workflows/${WF_ID}`);
	await page.waitForFunction(() => document.querySelectorAll('.svelte-flow__node').length === 4);
	await settle(page);
	await expect(page).toHaveScreenshot('editor-dark.png');
});

test('run history with stats', async ({ page }) => {
	await page.goto(`/workflows/${WF_ID}/runs`);
	await page.waitForSelector('text=Total runs');
	await page.waitForSelector('a[href^="/runs/"]');
	await settle(page);
	await expect(page).toHaveScreenshot('run-history.png');
});

test('run detail — canvas + task timeline', async ({ page }) => {
	await page.goto(`/runs/${RUN_ID}`);
	await page.waitForFunction(() => document.querySelectorAll('.svelte-flow__node').length === 4);
	await page.waitForSelector('aside:has-text("title #2")');
	await settle(page);
	await expect(page).toHaveScreenshot('run-detail-tasks.png');
});

test('run detail — logs tab', async ({ page }) => {
	await page.goto(`/runs/${RUN_ID}`);
	await page.waitForFunction(() => document.querySelectorAll('.svelte-flow__node').length === 4);
	await page.click('aside button:has-text("Logs")');
	await page.waitForSelector('text=newest first');
	await page.waitForFunction(
		() => document.querySelectorAll('aside div.flex.items-baseline').length >= 8
	);
	await settle(page);
	await expect(page).toHaveScreenshot('run-detail-logs.png');
});

test('run detail — attempt panel, dark', async ({ page }) => {
	await page.emulateMedia({ colorScheme: 'dark' });
	await page.goto(`/runs/${RUN_ID}`);
	await page.waitForFunction(() => document.querySelectorAll('.svelte-flow__node').length === 4);
	await page.click('.svelte-flow__node:has-text("title")');
	await page.waitForSelector('text=Attempt 2');
	await settle(page);
	await expect(page).toHaveScreenshot('run-detail-attempts-dark.png');
});

test('mcp servers list', async ({ page }) => {
	await page.goto('/mcp');
	await page.waitForSelector('text=github-tools');
	await page.waitForSelector('text=3 tools'); // live discovery resolved
	await settle(page);
	await expect(page).toHaveScreenshot('mcp-list.png');
});

test('mcp server dialog — dark', async ({ page }) => {
	await page.emulateMedia({ colorScheme: 'dark' });
	await page.goto('/mcp');
	await page.waitForSelector('text=github-tools');
	await page.click('button:has-text("Add server")');
	await page.waitForSelector('#mcp-url');
	await settle(page);
	await expect(page).toHaveScreenshot('mcp-dialog-dark.png');
});

test('configuration — secrets list', async ({ page }) => {
	await page.goto('/configuration');
	await page.waitForSelector('text=my_anthropic');
	await page.waitForSelector('text=webhook_token');
	await settle(page);
	await expect(page).toHaveScreenshot('configuration-secrets.png');
});
