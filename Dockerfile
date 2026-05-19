FROM golang:1.23 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o whereisit .

FROM alpine:3.20

RUN apk --no-cache add ca-certificates && \
    addgroup -S whereisit && adduser -S whereisit -G whereisit

WORKDIR /app

COPY --from=builder /app/whereisit /app/whereisit
COPY public/ /app/public/
COPY whereisit.ini /etc/whereisit.ini

USER whereisit

EXPOSE 8180

CMD ["/app/whereisit"]
