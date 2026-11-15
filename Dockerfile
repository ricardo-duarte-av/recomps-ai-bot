FROM golang:1.27-alpine AS build
RUN apk add --no-cache gcc musl-dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -tags goolm -trimpath -ldflags="-s -w" -o /out/recompbot ./cmd/recompbot

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 1000 -h /data recompbot
COPY --from=build /out/recompbot /usr/local/bin/recompbot
USER recompbot
WORKDIR /data
VOLUME ["/data"]
ENTRYPOINT ["recompbot"]
CMD ["-config", "/config/config.yaml"]
