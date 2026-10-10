# syntax=docker/dockerfile:1
FROM golang:1.26.2-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/bike-api ./cmd/bike-api

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=builder /out/bike-api /app/bike-api
ENV PORT=8081
EXPOSE 8081
USER nonroot:nonroot
ENTRYPOINT ["/app/bike-api"]
