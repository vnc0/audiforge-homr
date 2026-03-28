FROM golang:1.24 AS go-builder

WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/audiforge-homr .

FROM python:3.11-slim-bookworm

ENV PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    LOG=""

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates \
        fonts-dejavu-core \
        git \
        libgl1 \
        libglib2.0-0 \
        libgomp1 \
        poppler-utils && \
    rm -rf /var/lib/apt/lists/*

RUN pip install --no-cache-dir \
    git+https://github.com/liebharc/homr.git \
    git+https://github.com/papoteur-mga/relieur.git

RUN homr --init

WORKDIR /app
COPY --from=go-builder /out/audiforge-homr /app/audiforge-homr
COPY --from=go-builder /app/templates /app/templates

RUN mkdir -p /tmp/uploads /tmp/downloads && \
    chmod -R 755 /tmp/uploads /tmp/downloads /app/templates /app/audiforge-homr

EXPOSE 8080

ENTRYPOINT ["/app/audiforge-homr"]
