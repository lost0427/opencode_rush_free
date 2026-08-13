const port = Number(Bun.env.BUN_BRIDGE_PORT ?? 8787)
function header(request: Request, name: string) {
  return request.headers.get(name) ?? ""
}

function upstreamHeaders(request: Request) {
  const encoded = header(request, "X-Bun-Headers")
  const value = JSON.parse(Buffer.from(encoded, "base64").toString()) as Record<string, string[]>
  const headers = new Headers()
  for (const [name, values] of Object.entries(value)) for (const item of values) headers.append(name, item)
  return headers
}

function proxy(request: Request) {
  const value = header(request, "X-Bun-Proxy-URL")
  if (!value) return undefined
  if (!/^(https?|socks5h?):\/\//i.test(value)) throw new Error("unsupported proxy URL")
  const parsed = new URL(value)
  const username = header(request, "X-Bun-Proxy-Username")
  const password = header(request, "X-Bun-Proxy-Password")
  if (username) parsed.username = username
  if (password) parsed.password = password
  return parsed.toString()
}

Bun.serve({
  hostname: "127.0.0.1",
  port,
  async fetch(request) {
    if (request.method === "GET" && new URL(request.url).pathname === "/healthz") return new Response("ok")
    if (request.method !== "POST" || new URL(request.url).pathname !== "/request") return new Response("not found", { status: 404 })
    const target = header(request, "X-Bun-Target-URL")
    if (!/^https?:\/\//i.test(target)) return new Response("invalid target URL", { status: 400 })
    let proxyURL: string | undefined
    try {
      proxyURL = proxy(request)
    } catch (error) {
      return new Response(error instanceof Error ? error.message : "invalid proxy URL", { status: 400 })
    }

    let headers: Headers
    try {
      headers = upstreamHeaders(request)
    } catch {
      return new Response("invalid upstream headers", { status: 400 })
    }
    const body = await request.arrayBuffer()
    try {
      const response = await fetch(target, {
        method: header(request, "X-Bun-Method") || "GET",
        headers,
        body: body.byteLength ? body : undefined,
        proxy: proxyURL,
      } as RequestInit)
      return new Response(response.body, { status: response.status, statusText: response.statusText, headers: response.headers })
    } catch (error) {
      return new Response(error instanceof Error ? error.message : String(error), { status: 502, headers: { "X-Bun-Bridge-Error": "1" } })
    }
  },
})
