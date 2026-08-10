import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App, { ClientKeysPanel, Models, Proxies, Usage } from "./main";

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), {
  status,
  headers: { "Content-Type": "application/json" },
});

const summary = {
  requests: 0, counted_requests: 0, external_requests: 0, success: 0,
  success_rate: 0, prompt_tokens: 0, completion_tokens: 0, total_tokens: 0,
  free_models: 0, active_proxies: 0,
};

beforeEach(() => {
  vi.stubGlobal("fetch", vi.fn());
  vi.stubGlobal("confirm", vi.fn(() => true));
  history.replaceState(null, "", "/");
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("console feedback", () => {
  it("returns to login on an expired session", async () => {
    vi.mocked(fetch).mockResolvedValueOnce(json({}));
    vi.mocked(fetch).mockImplementation(async (input) => {
      const path = String(input);
      if (path.includes("/api/stats/summary")) return json(summary);
      if (path.includes("/api/models/free")) return json([]);
      if (path.includes("/api/usage/requests")) return json([]);
      return json({});
    });
    render(<App />);
    await screen.findByText("实时网关监控");
    window.dispatchEvent(new Event("relaydesk:auth-expired"));
    expect(await screen.findByText("进入控制台")).toBeInTheDocument();
  });

  it("uses a warning toast when part of the dashboard refresh fails", async () => {
    vi.mocked(fetch).mockResolvedValueOnce(json({}));
    vi.mocked(fetch).mockImplementation(async (input) => {
      const path = String(input);
      if (path.includes("/api/stats/summary")) return json({ error: "summary unavailable" }, 500);
      if (path.includes("/api/models/free")) return json([]);
      if (path.includes("/api/usage/requests")) return json([]);
      return json({});
    });
    render(<App />);
    const toast = await screen.findByText("部分控制台数据未能刷新");
    expect(toast.closest(".toast")).toHaveClass("warning");
  });
});

describe("operator workflows", () => {
  it("shows line-level proxy import results", async () => {
    vi.mocked(fetch).mockImplementation(async (input, init) => {
      const path = String(input);
      if (path.includes("/api/proxies?") || path.includes("/api/proxies?")) return json({ items: [], page: 1, page_size: 50, total: 0, total_pages: 1 });
      if (path.includes("/api/settings/proxy-engine")) return json({ engine: "builtin", effective_engine: "builtin", resin_platform: "Default", resin_configured: false, has_resin_proxy_token: false, resin_fallback_active: false });
      if (path === "/api/proxies/import" && init?.method === "POST") return json({ results: [
        { line: 1, uri: "http://one.example:8080", status: "imported" },
        { line: 2, uri: "bad-proxy", status: "invalid", error: "unsupported proxy URI" },
      ] });
      return json({});
    });
    render(<Proxies upstream={{}} reload={async () => {}} notify={vi.fn()} refreshToken={0} />);
    fireEvent.change(document.querySelector("textarea")!, { target: { value: "http://one.example:8080\nbad-proxy" } });
    fireEvent.click(screen.getByRole("button", { name: "导入代理" }));
    expect(await screen.findByText("第 2 行")).toBeInTheDocument();
    expect(screen.getByText("unsupported proxy URI")).toBeInTheDocument();
  });

  it("deletes a named client key rather than only disabling it", async () => {
    const notify = vi.fn();
    vi.mocked(fetch).mockImplementation(async (input, init) => {
      const path = String(input);
      if (path === "/api/client-keys" && (!init?.method || init.method === "GET")) return json([
        { id: 1, name: "Legacy", hint: "legacy", enabled: true, rpm_limit: 0, tpm_limit: 0 },
        { id: 2, name: "CI", hint: "ocp...test", enabled: true, rpm_limit: 10, tpm_limit: 0 },
      ]);
      if (path === "/api/client-keys/2" && init?.method === "DELETE") return json({ ok: true });
      return json({});
    });
    render(<ClientKeysPanel notify={notify} />);
    await screen.findByText("CI");
    fireEvent.click(screen.getByText("CI").closest(".key-row")!.querySelector("button[title='删除 Key']")!);
    await waitFor(() => expect(vi.mocked(fetch)).toHaveBeenCalledWith("/api/client-keys/2", expect.objectContaining({ method: "DELETE" })));
  });

  it("updates the model policy from the model status control", async () => {
    const reload = vi.fn();
    vi.mocked(fetch).mockImplementation(async (input, init) => {
      const path = String(input);
      if (path === "/api/model-aliases") return json([]);
      if (path === "/api/client-keys") return json([]);
      if (path === "/api/models/7/policy" && init?.method === "PATCH") return json({ model_id: "alpha:free", enabled: false });
      return json({});
    });
    render(<Models models={[{ id: 7, model_id: "alpha:free", display_name: "Alpha", is_free: true, free_reason: "test", refreshed_at: "2026-01-01T00:00:00Z", admin_enabled: true }]} upstream={{}} reload={reload} notify={vi.fn()} />);
    fireEvent.click(await screen.findByTitle("停用模型"));
    await waitFor(() => expect(vi.mocked(fetch)).toHaveBeenCalledWith("/api/models/7/policy", expect.objectContaining({ method: "PATCH" })));
  });

  it("persists applied usage filters in the URL", async () => {
    vi.mocked(fetch).mockImplementation(async (input) => {
      const path = String(input);
      if (path.includes("/api/usage/requests")) return json({ items: [], page: 1, page_size: 25, total: 0, total_pages: 1, models: [] });
      if (path.includes("/api/usage/rates")) return json({ window_seconds: 60, rpm: 0, tpm: 0, measured_at: "" });
      if (path.includes("/api/stats/timeseries")) return json([]);
      return json({});
    });
    render(<Usage notify={vi.fn()} onLogout={vi.fn()} refreshToken={0} />);
    await waitFor(() => expect(screen.getByRole("button", { name: "应用筛选" })).not.toBeDisabled());
    fireEvent.click(screen.getByRole("button", { name: "近 24 小时" }));
    fireEvent.click(screen.getByRole("button", { name: "应用筛选" }));
    await waitFor(() => expect(location.search).toContain("usage_time=24h"));
  });
});
