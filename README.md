# Relay Desk

Relay Desk 是一个面向 OpenCode Free 模型的 OpenAI 兼容网关。它把上游模型请求通过 HTTP、HTTPS 或 SOCKS5 代理池轮询转发，并提供一个带密码的 Web 控制台。

## 本地运行

后端需要 Go 1.26.5+，前端需要 Node 22+：

```powershell
go test ./...
cd web
npm ci
npm run dev
```

生产静态资源构建：

```powershell
cd web
npm run build
cd ..
go run ./cmd/server
```

服务没有默认管理密码。首次启动前必须设置 8 至 72 字节的 `ADMIN_PASSWORD`，并为 `APP_ENCRYPTION_KEY` 和 `SESSION_SECRET` 分别生成至少 32 字符的随机值；缺失、过短或使用已知占位密码时服务会拒绝初始化空数据库。

## Docker Compose

复制环境变量模板，填写管理员密码并生成随机密钥：

```bash
cp .env.example .env
# 编辑 ADMIN_PASSWORD，然后分别执行两次 openssl rand -base64 48
# 将结果填入 APP_ENCRYPTION_KEY 和 SESSION_SECRET
docker compose up -d --build
```

控制台和网关默认只绑定到 `http://127.0.0.1:8080`。需要监听其他地址时显式修改 `BIND_ADDRESS`，并通过防火墙限制访问。SQLite 数据库位于 Compose volume `opencode_data`，升级前可备份：

```bash
docker compose exec opencode-proxy cp /data/app.db /data/app.db.backup
```

生产环境建议在 Caddy、Nginx 或 Traefik 后面启用 HTTPS，并将 `COOKIE_SECURE=true`。`APP_ENCRYPTION_KEY` 用于解密数据库中已有的上游和代理凭据，不能直接替换；轮换时必须先完成密文迁移或准备重新录入这些凭据。修改管理员密码会撤销全部控制台会话并要求重新登录。

## 首次配置

1. 使用管理员密码登录控制台。
2. 在“代理池”粘贴多行代理 URI 并导入。导入时可选择永久、1/7/30/90 天或自定义到期时间；到期代理会自动删除，也可以勾选后批量删除。
3. 在“模型与网关”填写上游 Base URL 和 API Key。可在“自定义请求头”中按每行 `Header-Name: value` 填写上游请求头。
4. 默认会使用以下 OpenCode 标识，可按需修改或清空后保存：

   ```text
   User-Agent: opencode/1.18.12 ai-sdk/provider-utils/4.0.23 runtime/bun/1.3.13
   x-opencode-client: cli
   ```

   这些头会同时用于上游模型列表刷新和聊天转发。`Authorization`、`Content-Type`、`Host`、`Content-Length` 等由网关管理，不能作为自定义头覆盖。
5. 点击“刷新模型”。模型 ID 以 `:free` 或 `-free` 结尾，或 pricing 输入/输出均为 0 的模型会被暴露。
6. 点击“轮换 Key”，复制一次性显示的客户端 Key。
7. 在 OpenCode 中把兼容地址设置为：

   ```text
   http://your-host:8080/v1
   ```

    并使用生成的客户端 Key。

图片辅助模型（可选）：如果请求包含 `image_url`，而选中的 Free 模型只支持文本，可以在“图片辅助模型”中填写另一家供应商的 Base URL、API Key 和多模态模型 ID。网关会先让该独立模型生成图片描述，再把描述交给原来的文本模型；留空三项即可关闭。辅助供应商的凭证与 OpenCode 上游凭证分别加密保存。

网关接口：

```text
GET  /v1/models
POST /v1/chat/completions
```

请求需要 `Authorization: Bearer <client-key>`。请求记录只保存模型、代理、状态、耗时、重试次数和上游返回的 usage，不保存 Prompt 或响应正文。

## 运行与告警

- **Resin 路由不会自动降级**：选择 Resin 后，网关故障会直接向客户端返回错误。控制台显示 `unknown`、`healthy` 或 `degraded`，以及最后检测时间和原因；旧的 recover 接口仅执行一次手动复测。
- **代理健康**：普通转发复用代理连接。连接、超时、代理认证和 CONNECT 故障按 1、2、4、8、16、30 分钟冷却；上游普通 4xx 不会惩罚代理。后台出口探测默认每 15 分钟执行一次，上游路径探测默认每 60 分钟执行一次。
- **批量探测**：代理页的“检测本页”会异步创建任务并显示逐行结果。任务和结果会在 SQLite 中保留 15 分钟；服务重启前未完成的任务会标记为已中断。
- **客户端 Key**：在“模型与网关”中可创建、停用、删除和轮换多个 Key，并设置 RPM/TPM 限额和到期时间。升级时原有 Key 会自动迁移为 `Legacy`，旧客户端无需修改配置。Key 明文只在创建或轮换时显示一次。
- **模型策略**：可停用单个 Free 模型并创建一个目标明确的别名。别名目标被停用或从上游模型列表消失时，会从 `/v1/models` 隐藏并拒绝请求，不会改投其他模型。
- **Webhook**：在“设置”中配置 Webhook URL、可选 HMAC 密钥、事件和阈值。投递使用持久化 outbox，按立即、1、5、30 分钟重试；请求附带 `X-RelayDesk-Signature: sha256=...`。

部署前请备份 SQLite 数据库。迁移是幂等的，新表和列可由升级前的二进制安全忽略；建议每个阶段发布后观察错误率、P95 延迟和 SQLite 锁等待，再继续下一阶段。
