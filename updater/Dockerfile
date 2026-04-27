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
RUN chmod +x /app/run-import.sh

ENTRYPOINT [ "/app/run-import.sh" ]