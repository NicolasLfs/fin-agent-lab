FROM golang:1.26.1-alpine AS builder

WORKDIR /app
COPY . .
RUN go mod download && go build -o /server .

FROM alpine:3.21
RUN apk --no-cache add ca-certificates
COPY --from=builder /server /usr/local/bin/server

EXPOSE 8080
CMD ["server"]
