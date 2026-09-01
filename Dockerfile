FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    unzip \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY aurion-orchestrator /app/aurion-orchestrator
RUN chmod +x /app/aurion-orchestrator

ENV DATA_DIR=/var/lib/aurion
VOLUME ["/var/lib/aurion"]

EXPOSE 8090

CMD ["/app/aurion-orchestrator"]