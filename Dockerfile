# syntax=docker/dockerfile:1
#
# One image runs the whole platform: the Go binary serves the API, the background worker
# and the built React app. The same image runs every role in docker-compose.yml
# (`serve`, `worker`, `migrate`, `seed`).

# ---- 1. Build the web app ---------------------------------------------------------------
FROM node:22-alpine AS web
WORKDIR /web
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY frontend/ ./
ARG VITE_APP_NAME=GotATicket
ENV VITE_APP_NAME=$VITE_APP_NAME
RUN npm run build

# ---- 2. Build the Go binary -------------------------------------------------------------
FROM golang:1.24-alpine AS api
RUN apk add --no-cache ca-certificates
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/gotaticket ./cmd/gotaticket \
    && mkdir -p /out/data/uploads /out/data/mail /out/data/sandbox

# ---- 3. Runtime: just the binary, the web app and CA certificates ------------------------
# The binary embeds the database migrations and the time zone database, so nothing else is
# needed. It runs as an unprivileged user.
FROM scratch
COPY --from=api /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=api /out/gotaticket /gotaticket
COPY --from=web /web/dist /web
COPY --from=api --chown=65532:65532 /out/data /data
USER 65532:65532
ENV HTTP_ADDR=:8080 \
    WEB_DIR=/web \
    STORAGE_DIR=/data/uploads \
    MAIL_DIR=/data/mail
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=4s --start-period=20s --retries=6 CMD ["/gotaticket", "healthcheck"]
ENTRYPOINT ["/gotaticket"]
CMD ["serve"]
