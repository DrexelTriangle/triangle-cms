FROM golang:1.24-alpine AS builder
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /bin/cms ./main.go

FROM alpine:3.21
WORKDIR /app

COPY --from=builder /bin/cms /app/cms
COPY certs /app/certs

EXPOSE 8080
ENTRYPOINT ["/app/cms"]
