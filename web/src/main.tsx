import { createRoot } from "react-dom/client";
import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Activity,
  ArrowUpRight,
  Check,
  ChevronLeft,
  ChevronRight,
  CircleGauge,
  Copy,
  Database,
  ImageIcon,
  KeyRound,
  LogOut,
  Network,
  RefreshCw,
  Server,
  Settings2,
  ShieldCheck,
  Trash2,
  Upload,
  X,
} from "lucide-react";
import "./styles.css";
import "./security.css";

type Summary = {
  requests: number;
  success: number;
  success_rate: number;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  free_models: number;
  active_proxies: number;
};
type Proxy = {
  id: number;
  uri: string;
  scheme: string;
  host: string;
  port: number;
  username?: string;
  enabled: boolean;
  health_status: string;
  failure_count: number;
  cooldown_until?: string;
  expires_at?: string;
};
type ProxyPage = {
  items: Proxy[];
  page: number;
  page_size: number;
  total: number;
  total_pages: number;
};
type Model = {
  id: number;
  model_id: string;
  display_name: string;
  is_free: boolean;
  free_reason: string;
  refreshed_at: string;
};
type RequestRow = {
  id: number;
  created_at: string;
  model: string;
  proxy_uri?: string;
  status: string;
  status_code: number;
  latency_ms: number;
  retry_count: number;
  total_tokens?: number;
  error_message?: string;
};

const api = async (path: string, init: RequestInit = {}) => {
  const r = await fetch(path, {
    credentials: "include",
    headers: { "Content-Type": "application/json", ...(init.headers || {}) },
    ...init,
  });
  if (r.status === 401) throw new Error("AUTH");
  const data = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(data.error || "请求失败");
  return data;
};
const fmt = (n: number) => new Intl.NumberFormat("zh-CN").format(n || 0);
const headersToText = (headers?: Record<string, string>) =>
  Object.entries(headers || {})
    .map(([name, value]) => `${name}: ${value}`)
    .join("\n");
const parseHeaderText = (text: string) => {
  const headers: Record<string, string> = {};
  for (const [index, raw] of text.split(/\r?\n/).entries()) {
    const line = raw.trim();
    if (!line) continue;
    const separator = line.indexOf(":");
    if (separator < 1)
      throw new Error(`第 ${index + 1} 行必须使用 Header-Name: value 格式`);
    const name = line.slice(0, separator).trim();
    const value = line.slice(separator + 1).trim();
    if (!name) throw new Error(`第 ${index + 1} 行缺少请求头名称`);
    headers[name] = value;
  }
  return headers;
};

function Login({ onLogin }: { onLogin: () => void }) {
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    try {
      await api("/api/auth/login", {
        method: "POST",
        body: JSON.stringify({ password }),
      });
      onLogin();
    } catch (err) {
      setError((err as Error).message);
    }
  };
  return (
    <main className="login-shell">
      <div className="login-mark">
        <span>R</span>
        <div>
          <b>Relay Desk</b>
          <small>Free model gateway</small>
        </div>
      </div>
      <form className="login-card" onSubmit={submit}>
        <div className="eyebrow">PRIVATE CONTROL PLANE</div>
        <h1>
          把请求，交给
          <br />
          <em>更好的路径。</em>
        </h1>
        <p>管理模型、代理与使用记录。所有上游凭证只在服务器侧保存。</p>
        <label>
          管理员密码
          <input
            autoFocus
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder="输入密码"
          />
        </label>
        {error && <div className="error-line">{error}</div>}
        <button className="primary wide" type="submit">
          <ShieldCheck size={17} />
          进入控制台
        </button>
      </form>
      <div className="login-foot">OpenAI-compatible relay · v0.1</div>
    </main>
  );
}

function App() {
  const [authed, setAuthed] = useState<boolean | null>(null);
  const [page, setPage] = useState("overview");
  const [toast, setToast] = useState("");
  useEffect(() => {
    api("/api/auth/me")
      .then(() => setAuthed(true))
      .catch(() => setAuthed(false));
  }, []);
  useEffect(() => {
    if (toast) {
      const t = setTimeout(() => setToast(""), 2600);
      return () => clearTimeout(t);
    }
  }, [toast]);
  if (authed === null)
    return (
      <div className="boot">
        <CircleGauge className="spin" />
        正在连接控制台
      </div>
    );
  if (!authed) return <Login onLogin={() => setAuthed(true)} />;
  return (
    <Console
      page={page}
      setPage={setPage}
      notify={setToast}
      onLogout={() => {
        api("/api/auth/logout", { method: "POST" }).finally(() =>
          setAuthed(false),
        );
      }}
      toast={toast}
    />
  );
}

function Console({
  page,
  setPage,
  notify,
  onLogout,
  toast,
}: {
  page: string;
  setPage: (v: string) => void;
  notify: (v: string) => void;
  onLogout: () => void;
  toast: string;
}) {
  const [summary, setSummary] = useState<Summary>({
    requests: 0,
    success: 0,
    success_rate: 0,
    prompt_tokens: 0,
    completion_tokens: 0,
    total_tokens: 0,
    free_models: 0,
    active_proxies: 0,
  });
  const [models, setModels] = useState<Model[]>([]);
  const [rows, setRows] = useState<RequestRow[]>([]);
  const [upstream, setUpstream] = useState<any>({});
  const [proxyRefreshToken, setProxyRefreshToken] = useState(0);
  const load = async () => {
    try {
      const [s, m, r, u] = await Promise.all([
        api("/api/stats/summary"),
        api("/api/models/free"),
        api("/api/usage/requests?limit=25"),
        api("/api/settings/upstream"),
      ]);
      setSummary(s);
      setModels(m);
      setRows(r);
      setUpstream(u);
    } catch (e) {
      if ((e as Error).message === "AUTH") onLogout();
    }
  };
  useEffect(() => {
    load();
  }, []);
  const nav = [
    ["overview", "Overview", "仪表盘", CircleGauge],
    ["proxies", "Proxy pool", "代理池", Network],
    ["models", "Free models", "模型与网关", Database],
    ["usage", "Usage log", "使用记录", Activity],
  ] as const;
  return (
    <div className="app-shell">
      <aside>
        <div className="brand">
          <div className="brand-icon">R</div>
          <div>
            <strong>Relay Desk</strong>
            <small>FREE MODEL GATEWAY</small>
          </div>
        </div>
        <div className="nav-label">CONTROL ROOM</div>
        <nav>
          {nav.map(([id, en, zh, Icon]) => (
            <button
              key={id}
              className={page === id ? "active" : ""}
              onClick={() => setPage(id)}
            >
              <Icon size={17} />
              <span>
                <b>{en}</b>
                <small>{zh}</small>
              </span>
              {page === id && <ChevronRight size={15} />}
            </button>
          ))}
        </nav>
        <div className="aside-note">
          <span className="pulse" />
          Gateway online
          <br />
          <small>SQLite · encrypted secrets</small>
        </div>
        <button className="logout" onClick={onLogout}>
          <LogOut size={16} />
          退出
        </button>
      </aside>
      <main className="main">
        <header>
          <div>
            <div className="crumb">
              CONTROL ROOM <ChevronRight size={13} />{" "}
              {nav.find((n) => n[0] === page)?.[1].toUpperCase()}
            </div>
            <h2>{nav.find((n) => n[0] === page)?.[2]}</h2>
          </div>
          <div className="header-actions">
            <span className="endpoint">
              <span className="dot green" /> :8080 /v1
            </span>
            <button
              className="icon-btn"
              title="刷新数据"
              onClick={() => {
                load();
                setProxyRefreshToken((current) => current + 1);
                notify("数据已刷新");
              }}
            >
              <RefreshCw size={17} />
            </button>
          </div>
        </header>
        {page === "overview" && (
          <Overview summary={summary} models={models} rows={rows} />
        )}{" "}
        {page === "proxies" && (
          <Proxies
            upstream={upstream}
            reload={load}
            notify={notify}
            refreshToken={proxyRefreshToken}
          />
        )}{" "}
        {page === "models" && (
          <Models
            models={models}
            upstream={upstream}
            reload={load}
            notify={notify}
          />
        )}{" "}
        {page === "usage" && <Usage rows={rows} />}{" "}
        {toast && (
          <div className="toast">
            <Check size={15} />
            {toast}
          </div>
        )}
      </main>
    </div>
  );
}

function Stat({
  label,
  value,
  detail,
  accent,
}: {
  label: string;
  value: string;
  detail: string;
  accent?: string;
}) {
  return (
    <div className="stat">
      <div className="stat-top">
        <span>{label}</span>
        <span className="stat-accent" style={{ color: accent }}>
          <ArrowUpRight size={14} />
        </span>
      </div>
      <strong>{value}</strong>
      <small>{detail}</small>
    </div>
  );
}
function Overview({
  summary,
  models,
  rows,
}: {
  summary: Summary;
  models: Model[];
  rows: RequestRow[];
}) {
  const success = summary.success_rate * 100;
  return (
    <div className="page">
      <section className="hero-band">
        <div>
          <div className="eyebrow">LIVE RELAY TELEMETRY</div>
          <h1>
            Requests move.
            <br />
            <em>Signals stay clear.</em>
          </h1>
          <p>
            一个安静、可追踪的 Free 模型通道。代理池负责路径，控制台负责判断。
          </p>
        </div>
        <div className="hero-orbit">
          <div className="orbit-ring r1" />
          <div className="orbit-ring r2" />
          <div className="orbit-core">
            <span className="pulse" />
            LIVE
          </div>
        </div>
      </section>
      <section className="stat-grid">
        <Stat
          label="Total requests"
          value={fmt(summary.requests)}
          detail={`${success.toFixed(1)}% success`}
          accent="#c9684a"
        />
        <Stat
          label="Total tokens"
          value={fmt(summary.total_tokens)}
          detail={`${fmt(summary.prompt_tokens)} prompt · ${fmt(summary.completion_tokens)} completion`}
          accent="#416b5a"
        />
        <Stat
          label="Free models"
          value={fmt(summary.free_models)}
          detail="available to gateway"
          accent="#c49b3a"
        />
        <Stat
          label="Active proxies"
          value={fmt(summary.active_proxies)}
          detail="ready for rotation"
          accent="#5d78a4"
        />
      </section>
      <section className="two-col">
        <div className="panel">
          <div className="panel-head">
            <div>
              <span className="eyebrow">ROUTING SURFACE</span>
              <h3>Free models</h3>
            </div>
            <span className="count-chip">{models.length} online</span>
          </div>
          {models.length === 0 ? (
            <Empty text="还没有可用的 Free 模型" />
          ) : (
            <div className="model-list">
              {models.slice(0, 6).map((m) => (
                <div className="model-row" key={m.model_id}>
                  <span className="model-swatch">✦</span>
                  <div>
                    <b>{m.model_id}</b>
                    <small>
                      {m.free_reason.replaceAll("_", " ")} · refreshed{" "}
                      {new Date(m.refreshed_at).toLocaleDateString()}
                    </small>
                  </div>
                  <span className="status-tag">FREE</span>
                </div>
              ))}
            </div>
          )}
        </div>
        <div className="panel">
          <div className="panel-head">
            <div>
              <span className="eyebrow">RECENT TRAFFIC</span>
              <h3>Latest requests</h3>
            </div>
            <span className="count-chip">{rows.length} shown</span>
          </div>
          {rows.length === 0 ? (
            <Empty text="请求记录会出现在这里" />
          ) : (
            <div className="mini-table">
              {rows.slice(0, 5).map((r) => (
                <div className="mini-row" key={r.id}>
                  <span
                    className={`status-dot ${r.status === "success" ? "ok" : "bad"}`}
                  />
                  <div>
                    <b>{r.model}</b>
                    <small>
                      {new Date(r.created_at).toLocaleTimeString()} ·{" "}
                      {r.proxy_uri || "direct"}
                    </small>
                  </div>
                  <strong>{r.total_tokens ? fmt(r.total_tokens) : "—"}</strong>
                </div>
              ))}
            </div>
          )}
        </div>
      </section>
    </div>
  );
}

function Proxies({
  upstream,
  reload,
  notify,
  refreshToken,
}: {
  upstream: any;
  reload: () => Promise<void>;
  notify: (v: string) => void;
  refreshToken: number;
}) {
  const [text, setText] = useState("");
  const [expiry, setExpiry] = useState("0");
  const [customExpiry, setCustomExpiry] = useState("");
  const [proxies, setProxies] = useState<Proxy[]>([]);
  const [proxyPage, setProxyPage] = useState(1);
  const [pageSize, setPageSize] = useState(50);
  const [total, setTotal] = useState(0);
  const [totalPages, setTotalPages] = useState(1);
  const [loading, setLoading] = useState(true);
  const [selected, setSelected] = useState<number[]>([]);
  const [busy, setBusy] = useState(false);
  const [sessionLimit, setSessionLimit] = useState(
    String(upstream.session_proxy_request_limit ?? 50),
  );
  const loadProxies = useCallback(
    async (requestedPage: number, requestedPageSize: number) => {
      setLoading(true);
      try {
        const data = (await api(
          `/api/proxies?page=${requestedPage}&page_size=${requestedPageSize}`,
        )) as ProxyPage;
        setProxies(data.items);
        setProxyPage(data.page);
        setPageSize(data.page_size);
        setTotal(data.total);
        setTotalPages(data.total_pages);
        setSelected((current) =>
          current.filter((id) => data.items.some((proxy) => proxy.id === id)),
        );
      } catch (e) {
        notify((e as Error).message);
      } finally {
        setLoading(false);
      }
    },
    [notify],
  );
  useEffect(() => {
    void loadProxies(proxyPage, pageSize);
  }, [loadProxies, pageSize, proxyPage, refreshToken]);
  useEffect(
    () => setSessionLimit(String(upstream.session_proxy_request_limit ?? 50)),
    [upstream.session_proxy_request_limit],
  );
  const refreshAfterMutation = async (requestedPage = proxyPage) => {
    await reload();
    if (requestedPage !== proxyPage) {
      setProxyPage(requestedPage);
      return;
    }
    await loadProxies(requestedPage, pageSize);
  };
  const importIt = async () => {
    if (!text.trim()) return;
    const payload: any = { text };
    if (expiry === "custom") {
      if (!customExpiry) {
        notify("请选择到期时间");
        return;
      }
      payload.expires_at = new Date(customExpiry).toISOString();
    } else if (expiry !== "0") {
      payload.expires_in_days = Number(expiry);
    }
    setBusy(true);
    try {
      const d = await api("/api/proxies/import", {
        method: "POST",
        body: JSON.stringify(payload),
      });
      const ok = d.results.filter((x: any) => x.status === "imported").length;
      notify(`已导入 ${ok} 个代理${expiry === "0" ? "" : "，已设置有效期"}`);
      setText("");
      await refreshAfterMutation(1);
    } catch (e) {
      notify((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const toggle = async (p: Proxy) => {
    setBusy(true);
    try {
      await api(`/api/proxies/${p.id}`, {
        method: "PATCH",
        body: JSON.stringify({ enabled: !p.enabled }),
      });
      await refreshAfterMutation();
    } catch (e) {
      notify((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const remove = async (p: Proxy) => {
    if (!confirm(`删除 ${p.uri}？`)) return;
    setBusy(true);
    try {
      await api(`/api/proxies/${p.id}`, { method: "DELETE" });
      notify("代理已删除");
      await refreshAfterMutation();
    } catch (e) {
      notify((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const toggleSelected = (id: number) =>
    setSelected((current) =>
      current.includes(id) ? current.filter((x) => x !== id) : [...current, id],
    );
  const allSelected =
    proxies.length > 0 && proxies.every((proxy) => selected.includes(proxy.id));
  const bulkDelete = async () => {
    if (!selected.length || !confirm(`删除选中的 ${selected.length} 个代理？`))
      return;
    setBusy(true);
    try {
      const d = await api("/api/proxies/bulk-delete", {
        method: "POST",
        body: JSON.stringify({ ids: selected }),
      });
      notify(`已删除 ${d.deleted} 个代理`);
      setSelected([]);
      await refreshAfterMutation();
    } catch (e) {
      notify((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="page">
      <div className="page-intro">
        <div>
          <div className="eyebrow">NETWORK PATHS</div>
          <h1>代理池</h1>
          <p>用多条路径保持模型请求的可达性。支持 HTTP、HTTPS 与 SOCKS5。</p>
        </div>
        <div className="big-number">
          <strong>{fmt(total)}</strong>
          <small>managed paths</small>
        </div>
      </div>
      <section className="panel routing-panel">
        <div className="panel-head">
          <div>
            <h3>会话路由</h3>
            <p className="muted">
              同一会话固定使用一个代理；达到额度、收到上游 429
              或代理网络失败后才切换。
            </p>
          </div>
          <Network size={20} />
        </div>
        <div className="routing-controls">
          <label>
            单会话 / 单代理请求额度
            <input
              type="number"
              min="0"
              max="100000"
              value={sessionLimit}
              onChange={(e) => setSessionLimit(e.target.value)}
            />
            <small className="field-help">
              默认 50。设为 0 时不按请求次数切换，只在上游 429
              或代理故障时切换。
            </small>
          </label>
          <button
            className="secondary"
            disabled={busy}
            onClick={async () => {
              const limit = Number(sessionLimit);
              if (!Number.isInteger(limit) || limit < 0 || limit > 100000) {
                notify("请输入 0 到 100000 之间的整数");
                return;
              }
              setBusy(true);
              try {
                await api("/api/settings/routing", {
                  method: "PUT",
                  body: JSON.stringify({ session_proxy_request_limit: limit }),
                });
                notify("会话路由额度已保存");
                reload();
              } catch (e) {
                notify((e as Error).message);
              } finally {
                setBusy(false);
              }
            }}
          >
            保存额度
          </button>
        </div>
      </section>
      <section className="panel import-panel">
        <div className="panel-head">
          <div>
            <h3>批量导入</h3>
            <p className="muted">
              每行一个代理 URI，例如 http://user:pass@host:8080
            </p>
          </div>
          <Upload size={20} />
        </div>
        <textarea
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder={"http://10.0.0.2:8080\nsocks5://user:pass@10.0.0.3:1080"}
        />
        <div className="expiry-controls">
          <label>
            有效期
            <select value={expiry} onChange={(e) => setExpiry(e.target.value)}>
              <option value="0">永久</option>
              <option value="1">1 天</option>
              <option value="7">7 天</option>
              <option value="30">30 天</option>
              <option value="90">90 天</option>
              <option value="custom">自定义到期时间</option>
            </select>
          </label>
          {expiry === "custom" && (
            <label>
              到期时间
              <input
                type="datetime-local"
                value={customExpiry}
                onChange={(e) => setCustomExpiry(e.target.value)}
                min={new Date().toISOString().slice(0, 16)}
              />
            </label>
          )}
        </div>
        <button className="primary" onClick={importIt} disabled={busy}>
          <Upload size={16} />
          {busy ? "导入中…" : "导入代理"}
        </button>
      </section>
      <section className="panel">
        <div className="panel-head">
          <div>
            <h3>当前路径</h3>
            <p className="muted">失败路径会短暂冷却；到期代理会自动删除。</p>
          </div>
          <div className="proxy-actions">
            <label className="select-all">
              <input
                type="checkbox"
                checked={allSelected}
                onChange={(e) =>
                  setSelected(e.target.checked ? proxies.map((p) => p.id) : [])
                }
                disabled={!proxies.length || loading || busy}
              />
              <span>全选本页</span>
            </label>
            <button
              className="danger-button"
              onClick={bulkDelete}
              disabled={!selected.length || busy || loading}
            >
              <Trash2 size={14} />
              删除选中
            </button>
            <span className="count-chip">{fmt(total)} paths</span>
          </div>
        </div>
        <div className={`data-table ${loading ? "is-loading" : ""}`}>
          <div className="table-head">
            <span>URI</span>
            <span>状态</span>
            <span>失败次数</span>
            <span>操作</span>
          </div>
          {proxies.map((p) => (
            <div className="table-row" key={p.id}>
              <div className="proxy-uri-cell">
                <input
                  type="checkbox"
                  checked={selected.includes(p.id)}
                  onChange={() => toggleSelected(p.id)}
                  disabled={loading || busy}
                  aria-label={`选择 ${p.uri}`}
                />
                <div>
                  <b className="mono">{p.uri}</b>
                  <small>
                    {p.scheme.toUpperCase()} · {p.host}:{p.port}
                    {p.expires_at
                      ? ` · 到期 ${new Date(p.expires_at).toLocaleString()}`
                      : ""}
                  </small>
                </div>
              </div>
              <span className={`health ${p.health_status}`}>
                <i />
                {p.enabled
                  ? p.health_status === "cooldown"
                    ? "cooldown"
                    : "ready"
                  : "disabled"}
              </span>
              <span>{p.failure_count}</span>
              <div className="row-actions">
                <button
                  className="text-btn"
                  disabled={busy || loading}
                  onClick={() => toggle(p)}
                >
                  {p.enabled ? "禁用" : "启用"}
                </button>
                <button
                  className="danger-icon"
                  title="删除"
                  disabled={busy || loading}
                  onClick={() => remove(p)}
                >
                  <Trash2 size={15} />
                </button>
              </div>
            </div>
          ))}
          {loading && proxies.length === 0 && <Empty text="正在加载代理…" />}
          {!loading && proxies.length === 0 && (
            <Empty text="先导入第一条代理路径" />
          )}
        </div>
        <div className="proxy-pagination" aria-label="代理分页">
          <span className="pagination-range" aria-live="polite">
            {total === 0
              ? "0 / 0"
              : `${(proxyPage - 1) * pageSize + 1}-${Math.min(proxyPage * pageSize, total)} / ${fmt(total)}`}
          </span>
          <label className="page-size-control">
            <span>每页</span>
            <select
              value={pageSize}
              disabled={loading}
              onChange={(e) => {
                setPageSize(Number(e.target.value));
                setProxyPage(1);
              }}
            >
              <option value={50}>50</option>
              <option value={100}>100</option>
              <option value={200}>200</option>
            </select>
          </label>
          <div className="page-navigation">
            <button
              className="icon-btn"
              type="button"
              title="上一页"
              aria-label="上一页"
              disabled={loading || proxyPage <= 1}
              onClick={() =>
                setProxyPage((current) => Math.max(1, current - 1))
              }
            >
              <ChevronLeft size={16} />
            </button>
            <span className="page-indicator">
              第 {proxyPage} / {totalPages} 页
            </span>
            <button
              className="icon-btn"
              type="button"
              title="下一页"
              aria-label="下一页"
              disabled={loading || proxyPage >= totalPages}
              onClick={() =>
                setProxyPage((current) => Math.min(totalPages, current + 1))
              }
            >
              <ChevronRight size={16} />
            </button>
          </div>
        </div>
      </section>
    </div>
  );
}

function Models({
  models,
  upstream,
  reload,
  notify,
}: {
  models: Model[];
  upstream: any;
  reload: () => void;
  notify: (v: string) => void;
}) {
  const [base, setBase] = useState(upstream.base_url || "");
  const [key, setKey] = useState("");
  const [visionBase, setVisionBase] = useState(upstream.vision_base_url || "");
  const [visionKey, setVisionKey] = useState("");
  const [visionModel, setVisionModel] = useState(upstream.vision_model || "");
  const [headers, setHeaders] = useState(() =>
    headersToText(upstream.custom_headers),
  );
  const [clientKey, setClientKey] = useState("");
  const [busy, setBusy] = useState(false);
  const [testResult, setTestResult] = useState<any>(null);
  useEffect(() => {
    setBase(upstream.base_url || "");
    setVisionBase(upstream.vision_base_url || "");
    setVisionModel(upstream.vision_model || "");
    setHeaders(headersToText(upstream.custom_headers));
  }, [
    upstream.base_url,
    upstream.custom_headers,
    upstream.vision_base_url,
    upstream.vision_model,
  ]);
  const save = async () => {
    let customHeaders: Record<string, string>;
    try {
      customHeaders = parseHeaderText(headers);
    } catch (e) {
      notify((e as Error).message);
      return;
    }
    setBusy(true);
    try {
      await api("/api/settings/upstream", {
        method: "PUT",
        body: JSON.stringify({
          base_url: base,
          api_key: key,
          vision_base_url: visionBase,
          vision_api_key: visionKey,
          vision_model: visionModel,
          custom_headers: customHeaders,
        }),
      });
      notify("上游配置已保存");
      setKey("");
      setVisionKey("");
      reload();
    } catch (e) {
      notify((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const refresh = async () => {
    setBusy(true);
    try {
      const d = await api("/api/settings/models/refresh", { method: "POST" });
      notify(d.warning || `已刷新 ${d.free_model_count} 个 Free 模型`);
      reload();
    } catch (e) {
      notify((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const rotate = async () => {
    if (!confirm("轮换后旧客户端 Key 会立即失效，继续？")) return;
    try {
      const d = await api("/api/settings/client-key/rotate", {
        method: "POST",
      });
      setClientKey(d.client_key);
      notify("新 Key 已生成，请立即复制");
    } catch (e) {
      notify((e as Error).message);
    }
  };
  const testKey = async () => {
    setBusy(true);
    setTestResult(null);
    try {
      const d = await api("/api/settings/upstream/test", { method: "POST" });
      setTestResult(d);
      notify("上游 Key 测试完成");
    } catch (e) {
      notify((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const copy = () => {
    navigator.clipboard?.writeText(`${location.origin}/v1`);
    notify("网关地址已复制");
  };
  const copyKey = () => {
    navigator.clipboard?.writeText(clientKey);
    notify("客户端 Key 已复制");
  };
  return (
    <div className="page">
      <div className="page-intro">
        <div>
          <div className="eyebrow">MODEL DIRECTORY</div>
          <h1>模型与网关</h1>
          <p>从上游拉取模型，只把真正免费的路径交给客户端。</p>
        </div>
        <button className="primary" onClick={refresh} disabled={busy}>
          <RefreshCw size={16} />
          {busy ? "刷新中…" : "刷新模型"}
        </button>
      </div>
      <section className="config-grid">
        <div className="panel">
          <div className="panel-head">
            <div>
              <h3>上游端点</h3>
              <p className="muted">凭证只在服务器侧使用</p>
            </div>
            <Server size={20} />
          </div>
          <label>
            Base URL
            <input
              value={base}
              onChange={(e) => setBase(e.target.value)}
              placeholder="https://api.example.com"
            />
          </label>
          <label>
            API Key
            <input
              type="password"
              value={key}
              onChange={(e) => setKey(e.target.value)}
              placeholder={
                upstream.has_api_key
                  ? "已保存 · 留空保持不变"
                  : "输入上游 OpenCode / Zen Key"
              }
            />
            <small className="field-help">
              这里填写上游 OpenCode / Zen API Key，不是下面的网关 Key。粘贴带
              Bearer 前缀的值也会自动处理。
            </small>
          </label>
          <label>
            自定义请求头
            <textarea
              className="headers-input"
              value={headers}
              onChange={(e) => setHeaders(e.target.value)}
              spellCheck={false}
              placeholder={
                "User-Agent: opencode/1.18.12 ai-sdk/provider-utils/4.0.23 runtime/bun/1.3.13\nx-opencode-client: cli"
              }
            />
          </label>
          <p className="muted header-note">
            每行一个 <code>Header-Name: value</code>
            。模型刷新和聊天请求都会使用这些值。
          </p>
          <div className="config-actions">
            <button className="secondary" onClick={save} disabled={busy}>
              保存配置
            </button>
            <button className="secondary" onClick={testKey} disabled={busy}>
              测试上游 Key
            </button>
          </div>
          {testResult && (
            <div className="test-result">
              <b>测试模型：{testResult.model}</b>
              <span>
                直连：
                {testResult.direct?.status_code ||
                  testResult.direct?.status}{" "}
                {testResult.direct?.message || ""}
              </span>
              <span>
                代理：
                {testResult.proxy?.status_code || testResult.proxy?.status}{" "}
                {testResult.proxy?.message || ""}
              </span>
            </div>
          )}
          {upstream.last_model_refresh_error && (
            <div className="error-line">
              {upstream.last_model_refresh_error}
            </div>
          )}
        </div>
        <div className="panel gateway-card">
          <div className="eyebrow">CLIENT GATEWAY</div>
          <h3>给 OpenCode 的地址</h3>
          <div className="copy-field">
            <code>{location.origin}/v1</code>
            <button className="icon-btn" title="复制地址" onClick={copy}>
              <Copy size={15} />
            </button>
          </div>
          <div className="gateway-meta">
            <span>
              <KeyRound size={14} />
              客户端 Key
            </span>
            <button className="text-btn" onClick={rotate}>
              轮换 Key
            </button>
          </div>
          {clientKey && (
            <div className="copy-field">
              <code>{clientKey}</code>
              <button className="icon-btn" title="复制 Key" onClick={copyKey}>
                <Copy size={15} />
              </button>
            </div>
          )}
          <p className="muted small">
            这是给 OpenCode 客户端使用的网关 Key，以 ocp- 开头；不要把它填入上游
            API Key。Key 只在生成后展示一次。
          </p>
        </div>
      </section>
      <section className="panel vision-helper-panel">
        <div className="panel-head">
          <div>
            <span className="eyebrow">OPTIONAL VISION BRIDGE</span>
            <h3>图片辅助模型</h3>
            <p className="muted">
              使用独立供应商的多模态模型先描述图片，再交给纯文本模型处理。
            </p>
          </div>
          <ImageIcon size={20} />
        </div>
        <div className="vision-grid">
          <label>
            辅助 Base URL
            <input
              value={visionBase}
              onChange={(e) => setVisionBase(e.target.value)}
              placeholder="https://vision-provider.example.com"
            />
          </label>
          <label>
            辅助模型 ID
            <input
              value={visionModel}
              onChange={(e) => setVisionModel(e.target.value)}
              placeholder="provider/vision-model"
            />
          </label>
          <label>
            辅助 API Key
            <input
              type="password"
              value={visionKey}
              onChange={(e) => setVisionKey(e.target.value)}
              placeholder={
                upstream.has_vision_api_key
                  ? "已保存 · 留空保持不变"
                  : "输入辅助供应商 API Key"
              }
            />
          </label>
        </div>
        <p className="muted vision-helper-note">
          三项同时填写后启用。辅助请求不会使用 OpenCode 上游
          Key，也不会把图片内容写入使用记录；请点击上方“保存配置”生效。
        </p>
      </section>
      <PasswordCard notify={notify} />
      <section className="panel">
        <div className="panel-head">
          <div>
            <h3>Free 模型</h3>
            <p className="muted">
              {upstream.last_model_refresh_at
                ? `最近刷新 ${new Date(upstream.last_model_refresh_at).toLocaleString()}`
                : "尚未刷新"}
            </p>
          </div>
          <span className="count-chip">{models.length} available</span>
        </div>
        <div className="model-grid">
          {models.map((m) => (
            <div className="model-card" key={m.model_id}>
              <div className="model-card-top">
                <span className="model-swatch">✦</span>
                <span className="status-tag">FREE</span>
              </div>
              <b>{m.model_id}</b>
              <small>{m.display_name}</small>
              <span className="reason">
                {m.free_reason.replaceAll("_", " ")}
              </span>
            </div>
          ))}
          {models.length === 0 && <Empty text="刷新上游以发现 Free 模型" />}
        </div>
      </section>
    </div>
  );
}

function PasswordCard({ notify }: { notify: (v: string) => void }) {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [busy, setBusy] = useState(false);
  const submit = async () => {
    if (next.length < 8) {
      notify("新密码至少需要 8 个字符");
      return;
    }
    setBusy(true);
    try {
      await api("/api/auth/password", {
        method: "POST",
        body: JSON.stringify({ current, new: next }),
      });
      setCurrent("");
      setNext("");
      notify("管理员密码已更新，请重新登录");
      window.setTimeout(() => window.location.reload(), 500);
    } catch (e) {
      notify((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <section className="panel security-panel">
      <div className="panel-head">
        <div>
          <span className="eyebrow">SECURITY</span>
          <h3>管理员密码</h3>
          <p className="muted">修改后当前会话仍保持有效。</p>
        </div>
        <KeyRound size={20} />
      </div>
      <div className="password-grid">
        <label>
          当前密码
          <input
            type="password"
            value={current}
            onChange={(e) => setCurrent(e.target.value)}
          />
        </label>
        <label>
          新密码
          <input
            type="password"
            value={next}
            maxLength={72}
            onChange={(e) => setNext(e.target.value)}
            placeholder="至少 8 个字符"
          />
        </label>
        <button className="secondary" onClick={submit} disabled={busy}>
          更新密码
        </button>
      </div>
    </section>
  );
}

function Usage({ rows }: { rows: RequestRow[] }) {
  const [filter, setFilter] = useState("");
  const shown = useMemo(
    () =>
      rows.filter(
        (r) => !filter || r.model.toLowerCase().includes(filter.toLowerCase()),
      ),
    [rows, filter],
  );
  return (
    <div className="page">
      <div className="page-intro">
        <div>
          <div className="eyebrow">AUDIT TRAIL</div>
          <h1>使用记录</h1>
          <p>只记录请求元数据，保留路径与成本的可见性。</p>
        </div>
        <div className="search">
          <Activity size={15} />
          <input
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="筛选模型…"
          />
        </div>
      </div>
      <section className="panel">
        <div className="panel-head">
          <div>
            <h3>Requests</h3>
            <p className="muted">最近 25 条记录</p>
          </div>
          <span className="count-chip">{shown.length} shown</span>
        </div>
        <div className="data-table usage-table">
          <div className="table-head">
            <span>模型 / 时间</span>
            <span>结果</span>
            <span>路径</span>
            <span>Token</span>
            <span>延迟</span>
          </div>
          {shown.map((r) => (
            <div className="table-row" key={r.id}>
              <div>
                <b>{r.model}</b>
                <small>{new Date(r.created_at).toLocaleString()}</small>
              </div>
              <span
                className={`health ${r.status === "success" ? "healthy" : "cooldown"}`}
              >
                <i />
                {r.status === "success" ? "success" : `HTTP ${r.status_code}`}
                {r.error_message && (
                  <small className="request-error">{r.error_message}</small>
                )}
              </span>
              <span className="mono tiny">{r.proxy_uri || "direct"}</span>
              <span>{r.total_tokens ? fmt(r.total_tokens) : "—"}</span>
              <span>{r.latency_ms} ms</span>
            </div>
          ))}
          {shown.length === 0 && <Empty text="暂无匹配记录" />}
        </div>
      </section>
    </div>
  );
}
function Empty({ text }: { text: string }) {
  return (
    <div className="empty">
      <Database size={19} />
      <span>{text}</span>
    </div>
  );
}
export default App;

const root = document.getElementById("root");
if (!root) throw new Error("Root element not found");
createRoot(root).render(<App />);
