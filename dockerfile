FROM golang:1.26-alpine AS builder
RUN apk add --no-cache build-base
WORKDIR /app
COPY . .
RUN go mod download
RUN CGO_ENABLED=1 GOOS=linux GOARCH=$TARGETARCH go build -o lyrics-service main.go

FROM alpine:latest
RUN apk add --no-cache ca-certificates
WORKDIR /root/
COPY --from=builder /app/lyrics-service .
# Crea cartella data per il database
RUN mkdir /root/data
CMD ["./lyrics-service"]