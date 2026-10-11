FROM golang:1.26.2-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY services/maintenance-worker ./services/maintenance-worker
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/maintenance-worker ./services/maintenance-worker

FROM alpine:3.22
RUN addgroup -S app && adduser -S -G app app
COPY --from=build /out/maintenance-worker /usr/local/bin/maintenance-worker
USER app
EXPOSE 8082
ENTRYPOINT ["/usr/local/bin/maintenance-worker"]
