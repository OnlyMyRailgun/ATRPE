FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /usr/local/bin/atrpe-api ./cmd/api
RUN CGO_ENABLED=0 go build -o /usr/local/bin/atrpe-worker ./cmd/worker

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /usr/local/bin/atrpe-api /usr/local/bin/atrpe-api
COPY --from=builder /usr/local/bin/atrpe-worker /usr/local/bin/atrpe-worker
ENTRYPOINT ["/usr/local/bin/atrpe-worker"]
