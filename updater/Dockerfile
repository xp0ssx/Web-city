FROM golang:1.23-bookworm AS build

WORKDIR /src
COPY . .
RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/updater .

FROM debian:bookworm-slim

RUN apt-get update \
  && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    osm2pgsql \
    osmium-tool \
    postgresql-client \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY run-import.sh /app/run-import.sh
COPY config /app/config
RUN chmod +x /app/run-import.sh

COPY --from=build /bin/updater /bin/updater

ENTRYPOINT ["/bin/updater"]
