FROM --platform=$BUILDPLATFORM node:24-alpine AS admin-ui-build
WORKDIR /src/admin-ui
RUN corepack enable
COPY admin-ui/package.json admin-ui/pnpm-lock.yaml admin-ui/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY admin-ui/index.html admin-ui/tsconfig.json admin-ui/vite.config.ts ./
COPY admin-ui/src ./src
RUN pnpm build

FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
COPY --from=admin-ui-build /src/internal/server/web/admin ./internal/server/web/admin
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/fullpro-server ./cmd/fullpro-server

FROM alpine:3.24
RUN apk add --no-cache wget && adduser -D -H -u 10001 fullpro
WORKDIR /app
COPY --from=build /out/fullpro-server /usr/local/bin/fullpro-server
RUN mkdir -p /data /backups && chown -R fullpro:fullpro /data /backups
USER fullpro
ENV FULLPRO_ADDR=:8881
ENV FULLPRO_DB=/data/fullpro.db
ENV FULLPRO_BACKUP_DIRECTORY=/backups
ENV FULLPRO_COOKIE_SECURE=true
ENV FULLPRO_HEALTHCHECK_URL=http://127.0.0.1:8881/health/live
EXPOSE 8881
VOLUME ["/data", "/backups"]
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=30s --timeout=3s --start-period=30s --retries=3 \
	CMD wget -q -T 2 -O /dev/null "$FULLPRO_HEALTHCHECK_URL" || exit 1
CMD ["fullpro-server"]
