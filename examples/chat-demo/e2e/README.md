# GoToIM Console E2E Tests

Playwright-based E2E tests for the GoToIM frontend demo.

## Quick Start

```bash
# Install dependencies
cd examples/chat-demo/e2e
npm install

# Run all tests (headless, starts dev server automatically)
npm test

# Run with browser UI
npm run test:ui

# Run with headed browser
npm run test:headed

# Debug single test
npm run test:debug
```

## Prerequisites

- Node.js >= 18
- GoToIM backend services running (optional — most tests verify UI state)
  - Logic API at `http://localhost:3111` (or same-origin)
  - Comet WebSocket at `localhost:3102`

## Test Architecture

- **Page load & rendering**: Verify 3 default clients, config strip, all panels
- **Client management**: Add/edit/delete clients, quick presets, localStorage persistence
- **Connection lifecycle**: Connect/disconnect buttons, auto-reconnect config, status display
- **HTTP Push API**: Submit push form, verify timeline events
- **View filter**: User filtering dropdown
- **Search & filter**: Search input, filter panel toggle, filter options
- **Dark mode**: Theme toggle, data-theme attribute
- **Mobile responsive**: Mobile nav visibility, viewport adaptation
- **Metrics**: Dashboard controls, refresh interval options
- **Storage**: IndexedDB management UI, export buttons
- **Message trace**: Status filter options
- **Path stats**: Dual-channel visualization UI
- **Notifications**: Desktop notification and sound config checkboxes

## Manual Server

If you prefer running the dev server manually:

```bash
# From project root
npx serve . -p 8080 --no-clipboard

# Then run tests without auto-start
npx playwright test
```

## File Structure

```
e2e/
├── package.json          # Dependencies and scripts
├── playwright.config.js  # Playwright configuration
├── demo.spec.js          # Test cases
└── README.md             # This file
```
