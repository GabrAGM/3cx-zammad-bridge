FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /zammadbridge ./cmd/main.go

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /zammadbridge /usr/local/bin/zammadbridge
COPY config.yaml /etc/zammadbridge/config.yaml
ENTRYPOINT ["zammadbridge"]
CMD ["-config", "/etc/zammadbridge/config.yaml"]
