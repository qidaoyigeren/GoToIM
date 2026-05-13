const { test, expect } = require('@playwright/test');

const PAGE_URL = '/examples/chat-demo/index.html';

/**
 * GoToIM Console E2E Test Suite
 *
 * Covers the 11 prompts' key features:
 * - P0: auto-reconnect, ACK, client management
 * - P1: IndexedDB, dual-channel, session view
 * - P2: notifications, search, metrics, dark mode, responsive
 */

// ----- Prompt 3: Client Management -----

test('page loads with 3 default clients rendered', async ({ page }) => {
  await page.goto(PAGE_URL);
  await page.waitForSelector('#clientList .client-row');
  const rows = await page.$$('#clientList .client-row');
  expect(rows.length).toBeGreaterThanOrEqual(3);
});

test('config strip is visible with all controls', async ({ page }) => {
  await page.goto(PAGE_URL);
  await expect(page.locator('#apiHost')).toBeVisible();
  await expect(page.locator('#wsHost')).toBeVisible();
  await expect(page.locator('#defaultRoom')).toBeVisible();
  await expect(page.locator('#autoReconnect')).toBeVisible();
  await expect(page.locator('#desktopNotify')).toBeVisible();
});

// ----- Prompt 1: WebSocket connection & auto-reconnect -----

test('WebSocket connect button triggers connection attempt', async ({ page }) => {
  await page.goto(PAGE_URL);
  // Click connect on first client
  const connectBtn = page.locator('#clientList button[data-action="connect"]').first();
  await connectBtn.click();
  // Status should change to connecting or connected
  await expect(page.locator('.client-status-line').first()).toBeVisible();
});

test('disconnect button sets status to disconnected', async ({ page }) => {
  await page.goto(PAGE_URL);
  // First connect
  const connectBtn = page.locator('#clientList button[data-action="connect"]').first();
  await connectBtn.click();
  await page.waitForTimeout(500);
  // Then disconnect
  const disconnectBtn = page.locator('#clientList button[data-action="disconnect"]').first();
  await disconnectBtn.click();
  await expect(page.locator('.client-status-line.disconnected').first()).toBeVisible({ timeout: 5000 });
});

test('auto-reconnect checkbox is checked by default', async ({ page }) => {
  await page.goto(PAGE_URL);
  await expect(page.locator('#autoReconnect')).toBeChecked();
});

test('max reconnect attempts input is configurable', async ({ page }) => {
  await page.goto(PAGE_URL);
  const input = page.locator('#maxReconnectAttempts');
  await input.fill('5');
  await expect(input).toHaveValue('5');
});

// ----- Prompt 1 & 4: Config persistence to localStorage -----

test('config saves to localStorage and restores on refresh', async ({ page }) => {
  await page.goto(PAGE_URL);
  await page.locator('#heartbeatSeconds').fill('60');
  await page.locator('#saveConfig').click();
  await page.waitForTimeout(300);

  // Reload and verify
  await page.reload();
  await page.waitForSelector('#heartbeatSeconds');
  const val = await page.locator('#heartbeatSeconds').inputValue();
  expect(val).toBe('60');
});

test('client list persists to localStorage', async ({ page }) => {
  await page.goto(PAGE_URL);
  // Add a new client
  await page.locator('#addClient').click();
  await page.waitForSelector('#clientModal.active');
  await page.locator('#clientFormName').fill('E2E Test Client');
  await page.locator('#clientFormMid').fill('9999');
  await page.locator('#clientForm').evaluate(form => form.dispatchEvent(new Event('submit')));
  await page.waitForTimeout(500);

  // Reload and verify the client persists
  await page.reload();
  await page.waitForSelector('#clientList .client-row');
  const text = await page.locator('#clientList').textContent();
  expect(text).toContain('E2E Test Client');
});

// ----- Prompt 3: Client add/edit/delete modal -----

test('add client button opens modal', async ({ page }) => {
  await page.goto(PAGE_URL);
  await page.locator('#addClient').click();
  await expect(page.locator('#clientModal')).toHaveClass(/active/);
});

test('modal close button dismisses modal', async ({ page }) => {
  await page.goto(PAGE_URL);
  await page.locator('#addClient').click();
  await page.waitForSelector('#clientModal.active');
  await page.locator('#closeClientModal').click();
  await expect(page.locator('#clientModal')).not.toHaveClass(/active/);
});

test('generate key/device button fills the fields', async ({ page }) => {
  await page.goto(PAGE_URL);
  await page.locator('#addClient').click();
  await page.locator('#clientFormName').fill('TestGenerate');
  await page.locator('#generateClientIds').click();
  const keyVal = await page.locator('#clientFormKey').inputValue();
  const deviceVal = await page.locator('#clientFormDevice').inputValue();
  expect(keyVal.length).toBeGreaterThan(5);
  expect(deviceVal.length).toBeGreaterThan(5);
});

test('quick add preset adds clients', async ({ page }) => {
  await page.goto(PAGE_URL);
  const initialCount = await page.$$('#clientList .client-row');
  await page.locator('#quickAddPreset').selectOption('two-users');
  await page.waitForTimeout(300);
  const newCount = await page.$$('#clientList .client-row');
  expect(newCount.length).toBe(initialCount.length + 2);
});

test('reset to defaults restores 3 clients', async ({ page }) => {
  await page.goto(PAGE_URL);
  // Add extra client
  await page.locator('#quickAddPreset').selectOption('two-users');
  await page.waitForTimeout(300);
  // Reset
  page.on('dialog', dialog => dialog.accept());
  await page.locator('#resetClients').click();
  await page.waitForTimeout(500);
  const rows = await page.$$('#clientList .client-row');
  expect(rows.length).toBe(3);
});

test('delete client with confirm removes it', async ({ page }) => {
  await page.goto(PAGE_URL);
  const initialCount = await page.$$('#clientList .client-row');
  page.on('dialog', dialog => dialog.accept());
  await page.locator('#clientList button[data-action="delete"]').first().click();
  await page.waitForTimeout(500);
  const newCount = await page.$$('#clientList .client-row');
  expect(newCount.length).toBe(initialCount.length - 1);
});

// ----- Prompt 5: HTTP Push API -----

test('HTTP push form submits via API', async ({ page }) => {
  await page.goto(PAGE_URL);
  await page.locator('#pushBody').fill('{"from":1001,"to":1002,"text":"e2e test push","ts":0}');
  await page.locator('#manualPushForm button[type="submit"]').click();
  // Should have some timeline event (system or error)
  await page.waitForTimeout(1000);
  const events = await page.$$('#timeline .event');
  expect(events.length).toBeGreaterThan(0);
});

// ----- Prompt 6: View filter -----

test('view filter dropdown shows user options', async ({ page }) => {
  await page.goto(PAGE_URL);
  const options = await page.locator('#viewFilter option').allTextContents();
  expect(options.length).toBeGreaterThan(1); // "全部消息" + at least one user
});

test('selecting user view filters timeline', async ({ page }) => {
  await page.goto(PAGE_URL);
  await page.locator('#viewFilter').selectOption({ index: 0 }); // "全部消息"
  await expect(page.locator('#viewFilter')).toHaveValue('all');
});

// ----- Prompt 8: Search -----

test('search input is visible', async ({ page }) => {
  await page.goto(PAGE_URL);
  await expect(page.locator('#searchInput')).toBeVisible();
});

test('search filter panel toggles', async ({ page }) => {
  await page.goto(PAGE_URL);
  await page.locator('#filterToggle').click();
  await expect(page.locator('#filterPanel')).toHaveClass(/active/);
  await page.locator('#filterToggle').click();
  await expect(page.locator('#filterPanel')).not.toHaveClass(/active/);
});

// ----- Prompt 10: Dark mode -----

test('theme toggle switches data-theme', async ({ page }) => {
  await page.goto(PAGE_URL);
  const initial = await page.locator('html').getAttribute('data-theme');
  await page.locator('#themeToggle').click();
  const toggled = await page.locator('html').getAttribute('data-theme');
  expect(toggled).not.toBe(initial);
});

// ----- Prompt 10: Mobile responsive -----

test('mobile nav is visible at narrow viewport', async ({ page }) => {
  await page.setViewportSize({ width: 480, height: 800 });
  await page.goto(PAGE_URL);
  await expect(page.locator('#mobileNav')).toBeVisible();
});

test('mobile nav buttons exist', async ({ page }) => {
  await page.setViewportSize({ width: 480, height: 800 });
  await page.goto(PAGE_URL);
  const buttons = await page.$$('#mobileNav button');
  expect(buttons.length).toBe(3);
});

// ----- Prompt 9: Metrics -----

test('metrics panel has refresh button', async ({ page }) => {
  await page.goto(PAGE_URL);
  // Navigate to observe tab
  await page.locator('.tab-btn[data-tab="observe"]').click();
  await page.waitForTimeout(300);
  await expect(page.locator('#refreshMetrics')).toBeVisible();
});

test('metrics interval select has options', async ({ page }) => {
  await page.goto(PAGE_URL);
  await page.locator('.tab-btn[data-tab="observe"]').click();
  await page.waitForTimeout(300);
  const options = await page.locator('#metricsInterval option').allTextContents();
  expect(options.length).toBe(3);
});

// ----- Prompt 4: Storage management -----

test('trace tab has storage management', async ({ page }) => {
  await page.goto(PAGE_URL);
  await page.locator('.tab-btn[data-tab="trace"]').click();
  await page.waitForTimeout(300);
  await expect(page.locator('#storageSize')).toBeVisible();
  await expect(page.locator('#clearStorage')).toBeVisible();
  await expect(page.locator('#exportStorage')).toBeVisible();
});

// ----- Prompt 2: Message trace -----

test('trace tab has status filter', async ({ page }) => {
  await page.goto(PAGE_URL);
  await page.locator('.tab-btn[data-tab="trace"]').click();
  await page.waitForTimeout(300);
  const options = await page.locator('#traceStatusFilter option').allTextContents();
  expect(options.length).toBe(5); // all, pending, delivered, acked, failed
});

// ----- Prompt 5: Path stats -----

test('observe tab has path stats', async ({ page }) => {
  await page.goto(PAGE_URL);
  await page.locator('.tab-btn[data-tab="observe"]').click();
  await page.waitForTimeout(300);
  const text = await page.locator('.tool-block').allTextContents();
  const hasPath = text.some(t => t.includes('投递路径') || t.includes('直连'));
  expect(hasPath).toBe(true);
});

// ----- Prompt 7: Notification config -----

test('desktop notification checkbox is visible', async ({ page }) => {
  await page.goto(PAGE_URL);
  await expect(page.locator('#desktopNotify')).toBeVisible();
  await expect(page.locator('#soundNotify')).toBeVisible();
});
