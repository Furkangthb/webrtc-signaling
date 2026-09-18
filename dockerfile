FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o webrtc-app .

FROM alpine:latest
WORKDIR /app
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/webrtc-app .
COPY public ./public
RUN mkdir -p public/uploads kayitlar
EXPOSE 8080
CMD ["./webrtc-app"]