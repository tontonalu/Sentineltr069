# syntax=docker/dockerfile:1.7
# Multi-stage build — produz imagem mínima rodando como usuário não-root.

# ──────────────── Stage 1: builder ────────────────
FROM golang:1.25-alpine AS builder
WORKDIR /src

RUN apk add --no-cache git ca-certificates curl

# Cache de dependências
COPY go.mod go.sum* ./
RUN go mod download || true

# Código
COPY . .

# Geração de templates (templ)
RUN go install github.com/a-h/templ/cmd/templ@latest && \
    templ generate

# Tailwind CLI standalone (sem precisar instalar Node) gera web/static/css/app.css.
# htmx.min.js é baixado para web/static/js/. Ambos entram no //go:embed do binário.
RUN curl -fsSL -o /usr/local/bin/tailwindcss \
        "https://github.com/tailwindlabs/tailwindcss/releases/download/v3.4.17/tailwindcss-linux-x64" && \
    chmod +x /usr/local/bin/tailwindcss && \
    cd web && \
    /usr/local/bin/tailwindcss -i ./static/css/input.css -o ./static/css/app.css --minify && \
    cd .. && \
    mkdir -p web/static/js && \
    curl -fsSL -o web/static/js/htmx.min.js "https://unpkg.com/htmx.org@2.0.4/dist/htmx.min.js"

# Build dos 3 binários estáticos.
# -trimpath remove caminhos absolutos do binário (anti-forensics + reproducible builds).
# -s -w stripam tabela de símbolos e DWARF (anti-reverse-engineering trivial).
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION} -buildid=" \
    -o /out/server ./cmd/server && \
    CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION} -buildid=" \
    -o /out/worker ./cmd/worker && \
    CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -buildid=" \
    -o /out/migrate ./cmd/migrate

# ──────────────── Stage 2: runtime ────────────────
FROM alpine:3.20 AS runtime

# UID/GID 9001 fixo bate com o user 'sentinel' criado pelo bootstrap.sh
# no host — sem isso, volumes montados como /opt/sentinelacs/secrets/
# (perms 0750 sentinel:sentinel) ficam ilegíveis dentro do container.
RUN apk add --no-cache ca-certificates tzdata wget && \
    addgroup -g 9001 -S app && \
    adduser -u 9001 -S app -G app

WORKDIR /app

COPY --from=builder /out/server  /usr/local/bin/server
COPY --from=builder /out/worker  /usr/local/bin/worker
COPY --from=builder /out/migrate /usr/local/bin/migrate

# IMPORTANTE: migrations/*.sql e web/static/* NÃO são copiados.
# Estão embutidos nos binários via //go:embed (ver assets.go na raiz).
# No FS do container só existem os binários compilados — nada legível.

USER app
EXPOSE 8080

HEALTHCHECK --interval=15s --timeout=3s --start-period=20s --retries=3 \
  CMD wget -q --spider http://localhost:8080/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/server"]
