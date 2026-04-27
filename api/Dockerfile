FROM golang:1.23-alpine AS build

WORKDIR /app
COPY . .
RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/api ./cmd/api

FROM alpine:3.20
RUN adduser -D -H appuser
USER appuser

COPY --from=build /bin/api /bin/api
EXPOSE 8080

CMD ["/bin/api"]