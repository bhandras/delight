# syntax=docker/dockerfile:1

FROM golang:1.24.6-alpine AS builder

ARG TARGETOS
ARG TARGETARCH

RUN apk add --no-cache build-base

WORKDIR /src

# The server module depends on ../shared via a replace directive.
COPY shared ./shared
COPY server ./server

WORKDIR /src/server

RUN go mod download

RUN CGO_ENABLED=1 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
  go build -o /out/server ./cmd/server

FROM alpine:3.20

RUN adduser -D -u 10001 delight
USER delight

COPY --from=builder /out/server /server

EXPOSE 3005

ENTRYPOINT ["/server"]

