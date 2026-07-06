import { test, expect, type Page } from '@playwright/test';
import { mockApi, WF_ID, WF2_ID, RUN_ID, SKIP_RUN_ID, SUSPEND_RUN_ID } from './mock-api';

const CORS = {
	'access-control-allow-origin': 'http://localhost:4173',
	'access-control-allow-credentials': 'true'
};

// Visual regression suite. Any intentional UI change requires regenerating
// baselines with `bun run test:ui:update` and committing the diff — an
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

test('editor — version history panel', async ({ page }) => {
	await page.goto(`/workflows/${WF_ID}`);
	await page.waitForFunction(() => document.querySelectorAll('.svelte-flow__node').length === 4);
	await page.click('button[aria-label="Version history"]');
	await page.waitForSelector('text=Version history');
	await page.waitForSelector('text=current'); // the sheet's is_current badge
	await settle(page);
	await expect(page).toHaveScreenshot('version-history.png');
});

test('editor — triggers panel', async ({ page }) => {
	await page.goto(`/workflows/${WF_ID}`);
	await page.waitForFunction(() => document.querySelectorAll('.svelte-flow__node').length === 4);
	await page.click('button[aria-label="Triggers"]');
	await page.waitForSelector('text=*/5 * * * *'); // schedule summary
	await page.waitForSelector('text=/orders-hook'); // webhook summary
	await settle(page);
	await expect(page).toHaveScreenshot('triggers-panel.png');
});

test('editor — triggers panel, dark', async ({ page }) => {
	await page.emulateMedia({ colorScheme: 'dark' });
	await page.goto(`/workflows/${WF_ID}`);
	await page.waitForFunction(() => document.querySelectorAll('.svelte-flow__node').length === 4);
	await page.click('button[aria-label="Triggers"]');
	await page.waitForSelector('text=*/5 * * * *');
	await settle(page);
	await expect(page).toHaveScreenshot('triggers-panel-dark.png');
});

test('editor — add webhook trigger dialog', async ({ page }) => {
	await page.goto(`/workflows/${WF_ID}`);
	await page.waitForFunction(() => document.querySelectorAll('.svelte-flow__node').length === 4);
	await page.click('button[aria-label="Triggers"]');
	await page.click('button:has-text("Add trigger")');
	await page.click('button:has-text("Webhook")');
	await page.waitForSelector('input[placeholder="my-hook"]');
	await settle(page);
	await expect(page).toHaveScreenshot('triggers-add-webhook.png');
});

test('run detail — skipped node', async ({ page }) => {
	await page.goto(`/runs/${SKIP_RUN_ID}`);
	await page.waitForFunction(() => document.querySelectorAll('.svelte-flow__node').length === 3);
	await page.click('.svelte-flow__node:has-text("notify")');
	await page.waitForSelector('aside:has-text("Attempt 1")');
	await settle(page);
	await expect(page).toHaveScreenshot('run-detail-skipped.png');
});

test('editor inspector — ai.prompt dynamic selects', async ({ page }) => {
	await page.goto(`/workflows/${WF2_ID}`);
	await page.waitForFunction(() => document.querySelectorAll('.svelte-flow__node').length === 2);
	await page.click('.svelte-flow__node:has-text("summarize")');
	await page.waitForSelector('aside:has-text("API Key")');
	await page.waitForSelector('aside:has-text("my_anthropic")'); // api_key select populated
	await page.waitForSelector('aside:has-text("search_issues")'); // mcp_tool list resolved
	await settle(page);
	await expect(page).toHaveScreenshot('editor-inspector-ai.png');
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

test('mcp access — token list', async ({ page }) => {
	await page.goto('/api-access');
	await page.waitForSelector('text=MCP endpoint URL');
	await page.waitForSelector('text=claude-desktop');
	await page.waitForSelector('text=ci-runner');
	await settle(page);
	await expect(page).toHaveScreenshot('api-access-tokens.png');
});

test('mcp access — token list, dark', async ({ page }) => {
	await page.emulateMedia({ colorScheme: 'dark' });
	await page.goto('/api-access');
	await page.waitForSelector('text=claude-desktop');
	await settle(page);
	await expect(page).toHaveScreenshot('api-access-tokens-dark.png');
});

test('mcp access — empty state', async ({ page }) => {
	// Override just the tokens list to be empty (registered after mockApi, so it
	// wins). Everything else on the page stays mocked.
	await page.route('**/v1/api-tokens', (route) =>
		route.request().method() === 'GET'
			? route.fulfill({ status: 200, headers: { ...CORS, 'content-type': 'application/json' }, body: '[]' })
			: route.fallback()
	);
	await page.goto('/api-access');
	await page.waitForSelector('text=No tokens yet.');
	await settle(page);
	await expect(page).toHaveScreenshot('api-access-empty.png');
});

test('mcp access — token shown once dialog', async ({ page }) => {
	await page.goto('/api-access');
	await page.waitForSelector('text=claude-desktop');
	await page.click('button:has-text("Create token")');
	await page.fill('#token-name', 'my-agent');
	await page.locator('[role="dialog"] button[type="submit"]').click();
	await page.waitForSelector('text=Token created');
	await page.waitForSelector('text=Shown once');
	await settle(page);
	await expect(page).toHaveScreenshot('api-access-token-shown.png');
});

test('run detail — suspended node badge', async ({ page }) => {
	await page.goto(`/runs/${SUSPEND_RUN_ID}`);
	await page.waitForFunction(() => document.querySelectorAll('.svelte-flow__node').length === 3);
	await page.waitForSelector('.svelte-flow__node:has-text("waiting")'); // amber suspended label
	await settle(page);
	await expect(page).toHaveScreenshot('run-detail-suspended-node.png');
});

test('run detail — waiting-for-callback card', async ({ page }) => {
	await page.goto(`/runs/${SUSPEND_RUN_ID}`);
	await page.waitForFunction(() => document.querySelectorAll('.svelte-flow__node').length === 3);
	await page.click('.svelte-flow__node:has-text("approve")');
	await page.waitForSelector('text=Waiting for callback');
	await page.waitForSelector('text=Resume URL'); // resume URL block rendered
	await settle(page);
	await expect(page).toHaveScreenshot('run-detail-suspended-card.png');
});

test('run detail — waiting-for-callback card, dark', async ({ page }) => {
	await page.emulateMedia({ colorScheme: 'dark' });
	await page.goto(`/runs/${SUSPEND_RUN_ID}`);
	await page.waitForFunction(() => document.querySelectorAll('.svelte-flow__node').length === 3);
	await page.click('.svelte-flow__node:has-text("approve")');
	await page.waitForSelector('text=Waiting for callback');
	await settle(page);
	await expect(page).toHaveScreenshot('run-detail-suspended-card-dark.png');
});
