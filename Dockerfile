FROM golang:1.25 AS build

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY src ./src
RUN CGO_ENABLED=0 go build -o /out/polling-agent ./src

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends iputils-ping ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=build /out/polling-agent /usr/local/bin/polling-agent

USER nobody

ENTRYPOINT ["polling-agent"]
