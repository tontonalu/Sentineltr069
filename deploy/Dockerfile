# syntax=docker/dockerfile:1.7
# Multi-stage build — produz imagem mínima rodando como usuário não-root.

# ──────────────── Stage 1: builder ────────────────
FROM golang:1.25-alpine AS builder
WORKDIR /src

RUN apk add --no-cache git ca-certificates

# Cache de dependências
COPY go.mod go.sum* ./
RUN go mod download || true

# Código
COPY . .

# Geração de templates (templ)
RUN go install github.com/a-h/templ/cmd/templ@latest && \
    templ generate

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

RUN apk add --no-cache ca-certificates tzdata wget && \
    addgroup -S app && adduser -S app -G app

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
