FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -o lyrics-service main.go

FROM alpine:latest
WORKDIR /root/
COPY --from=builder /app/lyrics-service .
RUN mkdir /root/data
CMD ["./lyrics-service"]