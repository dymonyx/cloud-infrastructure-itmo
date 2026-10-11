FROM golang:1.26.2-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY services/bike-api ./services/bike-api
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/bike-api ./services/bike-api

FROM alpine:3.22
RUN addgroup -S app && adduser -S -G app app
COPY --from=build /out/bike-api /usr/local/bin/bike-api
USER app
EXPOSE 8081
ENTRYPOINT ["/usr/local/bin/bike-api"]
