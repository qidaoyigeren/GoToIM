const { defineConfig } = require('@playwright/test');

module.exports = defineConfig({
  testDir: '.',
  timeout: 30000,
  expect: { timeout: 10000 },
  fullyParallel: false,
  retries: 0,
  workers: 1,
  reporter: 'list',
  use: {
    baseURL: 'http://localhost:8080',
    trace: 'on-first-retry',
  },
  webServer: {
    command: 'npx --yes serve ../../ -p 8080 --no-clipboard',
    url: 'http://localhost:8080/examples/chat-demo/index.html',
    reuseExistingServer: true,
  },
});
