FROM golang:1.23-alpine AS build

WORKDIR /app
COPY . .
RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/api ./cmd/api

FROM alpine:3.20
RUN adduser -D -H appuser
WORKDIR /app
RUN mkdir -p /app/tile-cache && chown -R appuser:appuser /app/tile-cache
USER appuser

COPY --from=build /bin/api /bin/api
COPY --from=build /app/config /app/config
EXPOSE 8080

CMD ["/bin/api"]
