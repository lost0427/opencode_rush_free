import { defineConfig } from "@playwright/test";
import { tmpdir } from "node:os";
import { resolve } from "node:path";

const repoRoot = resolve(import.meta.dirname, "..");

export default defineConfig({
  testDir: "./e2e",
  timeout: 30_000,
  use: {
    baseURL: "http://127.0.0.1:4175",
    trace: "retain-on-failure",
  },
  webServer: {
    command: "go run ./cmd/server",
    cwd: repoRoot,
    url: "http://127.0.0.1:4175/healthz",
    reuseExistingServer: !process.env.CI,
    env: {
      PORT: "4175",
      WEB_DIR: resolve(repoRoot, "web", "dist"),
      DATABASE_PATH: resolve(tmpdir(), "relaydesk-playwright.db"),
      ADMIN_PASSWORD: "relaydesk-playwright-password",
      APP_ENCRYPTION_KEY: "relaydesk-playwright-encryption-key-000000000000",
      SESSION_SECRET: "relaydesk-playwright-session-secret-000000000000000",
    },
  },
});
