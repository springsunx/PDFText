FROM golang:1.27.1-trixie AS build

ARG APP_VERSION=0.1.0

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./
COPY src/pdftext/openapi.json ./src/pdftext/openapi.json
COPY src/pdftext/templates ./src/pdftext/templates
COPY src/pdftext/static ./src/pdftext/static

RUN CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${APP_VERSION}" \
    -o /out/pdftext .

FROM debian:trixie-slim

ARG APP_VERSION=0.1.0

LABEL org.opencontainers.image.title="PDFText" \
      org.opencontainers.image.description="Single-binary PDF text service with WebUI, REST API and MCP" \
      org.opencontainers.image.version="${APP_VERSION}" \
      org.opencontainers.image.source="https://github.com/springsunx/PDFText"

ENV PDFTEXT_ADDR=:8000 \
    PDFTEXT_MCP_ALLOWED_ROOT=/data/input

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates poppler-utils \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --create-home --uid 10001 --shell /usr/sbin/nologin app

COPY --from=build /out/pdftext /usr/local/bin/pdftext

USER app

EXPOSE 8000

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["pdftext", "healthcheck"]

ENTRYPOINT ["pdftext"]
CMD ["serve"]
