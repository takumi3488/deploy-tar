FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/main ./main.go

FROM gcr.io/distroless/static-debian12
COPY --from=builder /app/main /app/main
CMD ["/app/main"]
