FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -mod=mod -trimpath -ldflags="-s -w" -o /signaldesk ./cmd/signaldesk

FROM alpine:3.21
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 app
COPY --from=build /signaldesk /usr/local/bin/signaldesk
USER 10001
EXPOSE 8080
ENTRYPOINT ["signaldesk"]
CMD ["api"]
