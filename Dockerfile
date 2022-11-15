# This is a multi-stage build. First we are going to compile and then
# create a small image for runtime.
FROM golang:1.17-alpine as builder

RUN mkdir -p /app
WORKDIR /app
RUN apk add -U --no-cache ca-certificates
COPY go.mod go.sum ./


RUN go mod download

RUN adduser -D -u 10001 app
ENV GO111MODULE on
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -ldflags '-extldflags "-static"' -o ./sample-front .

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

COPY --from=builder /app/sample-front /main
COPY --from=builder /etc/passwd /etc/passwd

USER app

EXPOSE 8080
CMD ["/main"]
