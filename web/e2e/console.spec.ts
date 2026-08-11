import { expect, test, type Page } from "@playwright/test";

async function login(page: Page) {
  await page.goto("/");
  await page.getByLabel("管理员密码").fill("relaydesk-playwright-password");
  await page.getByRole("button", { name: "进入控制台" }).click();
  await expect(page.getByText("实时网关监控")).toBeVisible();
}

test("desktop console loads after login", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await login(page);
  await expect(page.getByRole("heading", { name: "仪表盘" })).toBeVisible();
});

test("mobile console keeps the dashboard reachable", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await login(page);
  await expect(page.getByText("请求在流动。")).toBeVisible();
});

test("public model status loads without login", async ({ page }) => {
  const buckets = Array.from({ length: 24 }, (_, index) => ({
    start: new Date(Date.UTC(2026, 7, 10, 7 + index)).toISOString(),
    requests: 1,
    success: 1,
    external_errors: 0,
    success_rate: 1,
    status: "available",
  }));
  await page.route("**/api/public/status", (route) => route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify({
      generated_at: "2026-08-11T07:00:00Z",
      timezone: "Asia/Shanghai",
      window_start: "2026-08-10T07:00:00Z",
      window_end: "2026-08-11T07:00:00Z",
      bucket_hours: 24,
      summary: { models: 1, requests_24h: 24, recent_requests_15m: 2, success_rate: 1, status: "available" },
      models: [{
        model_id: "alpha:free",
        display_name: "Alpha",
        admin_enabled: true,
        requests_24h: 24,
        recent_requests_15m: 2,
        success_24h: 24,
        external_errors_24h: 0,
        success_rate: 1,
        avg_latency_ms: 1200,
        avg_first_token_latency_ms: 400,
        status: "available",
        buckets,
      }],
    }),
  }));
  await page.goto("/status");
  await expect(page.getByRole("heading", { name: "全部模型" })).toBeVisible();
  await expect(page.getByText("Alpha", { exact: true })).toBeVisible();
  await expect(page.locator(".public-status-strip .public-status-cell")).toHaveCount(24);
  await expect(page.getByRole("textbox")).toHaveCount(0);
});
