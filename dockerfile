FROM --platform=$BUILDPLATFORM golang:1.22-alpine AS builder

ARG TARGETARCH

WORKDIR /app
COPY . .
RUN go mod download

RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -o lyrics-service main.go

FROM alpine:latest
RUN apk add --no-cache ca-certificates
WORKDIR /root/
COPY --from=builder /app/lyrics-service .

CMD ["./lyrics-service"]