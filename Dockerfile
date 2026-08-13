FROM node:22-alpine AS frontend
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26.5-alpine AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
COPY --from=frontend /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -o /out/opencode-proxy ./cmd/server

FROM oven/bun:1.3.14-alpine AS bun-bridge
WORKDIR /app
COPY bun-bridge/server.ts ./server.ts
RUN bun build --compile --outfile /out/bun-bridge ./server.ts

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=backend /out/opencode-proxy ./opencode-proxy
COPY --from=backend /src/web/dist ./web/dist
COPY --from=bun-bridge /out/bun-bridge ./bun-bridge
COPY bun-bridge/entrypoint.sh ./entrypoint.sh
RUN addgroup -S -g 10001 relaydesk && adduser -S -D -H -u 10001 -G relaydesk relaydesk \
    && mkdir -p /data \
    && chmod 0755 /app/entrypoint.sh \
    && chown -R relaydesk:relaydesk /data /app
ENV WEB_DIR=/app/web/dist DATABASE_PATH=/data/app.db PORT=8080
EXPOSE 8080
VOLUME ["/data"]
USER 10001:10001
CMD ["/app/entrypoint.sh"]
