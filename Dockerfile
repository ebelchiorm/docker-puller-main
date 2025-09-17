FROM --platform=$BUILDPLATFORM golang:1.21-alpine AS builder

WORKDIR /app
COPY . .

ARG TARGETOS
ARG TARGETARCH

RUN go mod tidy && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o puller

FROM alpine:3.19
COPY --from=builder /app/puller /usr/local/bin/

ENTRYPOINT ["/usr/local/bin/puller"]