FROM golang:1.26-alpine AS build
WORKDIR /src
ENV GOPROXY=https://goproxy.cn,direct
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# VERSION is stamped into the binary so /readyz and the console's platform
# status report which build an instance is running.
#   docker build --build-arg VERSION=$(git describe --tags --always) .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build \
      -ldflags="-s -w -X zekumo/internal/server.Version=${VERSION}" \
      -o /zekumo ./cmd/server

FROM alpine:3.20
# wget is only here for the healthcheck below; ca-certificates is needed for
# outbound HTTPS (S3 storage, webhook delivery, cloud-function fetches).
RUN apk add --no-cache ca-certificates wget && adduser -D -u 10001 app
USER app
COPY --from=build /zekumo /usr/local/bin/zekumo
EXPOSE 8080

# Readiness, not liveness: the container is only "healthy" once it can reach
# Postgres and Redis, so an orchestrator will not route traffic to it early.
HEALTHCHECK --interval=15s --timeout=5s --start-period=20s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/readyz >/dev/null || exit 1

ENTRYPOINT ["zekumo"]
