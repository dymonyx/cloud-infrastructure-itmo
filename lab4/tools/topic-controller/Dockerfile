FROM golang:1.26.2-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY tools/topic-controller ./tools/topic-controller
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/topic-controller ./tools/topic-controller

FROM alpine:3.22
RUN addgroup -S app && adduser -S -G app app
COPY --from=build /out/topic-controller /usr/local/bin/topic-controller
USER app
ENTRYPOINT ["/usr/local/bin/topic-controller"]
