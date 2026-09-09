FROM golang:1.26-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o webrtc-server main.go

FROM alpine:latest

WORKDIR /app

COPY --from=builder /app/webrtc-server .
COPY --from=builder /app/public ./public

EXPOSE 8080

CMD ["./webrtc-server"]