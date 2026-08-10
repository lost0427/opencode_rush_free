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
