FROM golang:1.26.1-alpine AS builder

WORKDIR /app
COPY . .
RUN go mod download && go build -o /server . && go build -o /market-worker ./cmd/market-worker

FROM alpine:3.21
RUN apk --no-cache add ca-certificates
COPY --from=builder /server /usr/local/bin/server
COPY --from=builder /market-worker /usr/local/bin/market-worker

EXPOSE 8080
CMD ["server"]
