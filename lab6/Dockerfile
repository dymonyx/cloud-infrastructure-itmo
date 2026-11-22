FROM golang:1.26.2-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/bike-api ./cmd/bike-api

FROM alpine:3.23

RUN apk add --no-cache ca-certificates wget \
    && addgroup -S chainwise \
    && adduser -S -G chainwise chainwise

COPY --from=builder /out/bike-api /usr/local/bin/bike-api

USER chainwise
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/bike-api"]
