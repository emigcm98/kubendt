ARG GO_VERSION=1.26.1
ARG ALPINE_VERSION=3.22

FROM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS build

RUN apk add --no-cache gcc musl-dev sqlite-dev && apk upgrade --no-cache

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go run github.com/swaggo/swag/cmd/swag@v1.16.6 init -g main.go

RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -o /kubendt .

FROM alpine:${ALPINE_VERSION}

# su-exec lets the entrypoint drop from root to the unprivileged user.
RUN apk add --no-cache ca-certificates sqlite-libs su-exec && apk upgrade --no-cache

# Non-root user the server actually runs as. Its home is /data (a volume), so
# any kubeconfig it writes there persists.
RUN addgroup -S kubendt && adduser -S -G kubendt -h /data kubendt

WORKDIR /app

COPY --from=build /kubendt /usr/local/bin/kubendt
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

# Version metadata, injected at build time by the release workflow.
# Defaults keep local/dev image builds honest (they report "dev"/"unknown").
ARG KUBENDT_VERSION=dev
ARG KUBENDT_COMMIT=unknown
ARG KUBENDT_BUILD_DATE=unknown
ENV KUBENDT_VERSION=${KUBENDT_VERSION} \
    KUBENDT_COMMIT=${KUBENDT_COMMIT} \
    KUBENDT_BUILD_DATE=${KUBENDT_BUILD_DATE}

ENV PORT=8080
ENV KUBENDT_DB_PATH=/data/kubendt.db
# Kubeconfig lives on the /data volume (user-owned, persistent). The entrypoint
# seeds it from a mounted /kube/config on first run.
ENV HOME=/data
ENV KUBECONFIG=/data/.kube/config

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["kubendt"]
