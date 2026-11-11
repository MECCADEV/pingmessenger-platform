import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './e2e',
  reporter: 'line',
  use: {
    baseURL: 'http://127.0.0.1:4173',
    headless: true,
    launchOptions: { executablePath: process.env.CHROME_PATH || '/usr/bin/google-chrome' },
  },
  webServer: {
    command: 'npm run preview -- --host 127.0.0.1 --port 4173 --strictPort',
    url: 'http://127.0.0.1:4173/admin/',
    reuseExistingServer: !process.env.CI,
    timeout: 30_000,
  },
});
