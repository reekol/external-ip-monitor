# Build a small static Linux binary, then run it in a minimal image.
FROM golang:1.27.2-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/external-ip-monitor .

FROM alpine:3.21
RUN apk --no-cache add ca-certificates \
    && addgroup -S monitor \
    && adduser -S -G monitor monitor \
    && mkdir -p /data \
    && chown monitor:monitor /data
WORKDIR /app
COPY --from=build /out/external-ip-monitor /usr/local/bin/external-ip-monitor
ENV IP_MONITOR_LOG_FILE=/data/ip-monitor.log
ENV IP_MONITOR_HISTORY_LIMIT=10000
ENV IP_MONITOR_PAGE_SIZES=10,25,50,100
ENV IP_MONITOR_DEFAULT_PAGE_SIZE=25
EXPOSE 8080
VOLUME ["/data"]
USER monitor
ENTRYPOINT ["/usr/local/bin/external-ip-monitor"]
