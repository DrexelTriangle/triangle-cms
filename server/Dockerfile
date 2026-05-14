FROM golang:1.25-alpine AS builder
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download
RUN go install github.com/swaggo/swag/cmd/swag@latest

COPY . .
RUN swag init --parseDependency --parseInternal
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /bin/cms ./main.go

FROM alpine:3.21
WORKDIR /app

COPY --from=builder /bin/cms /app/cms
COPY certs /app/certs

EXPOSE 8080
ENTRYPOINT ["/app/cms"]
