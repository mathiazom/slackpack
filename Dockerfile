FROM golang:1.21-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o slackpack .

FROM alpine:latest

RUN apk --no-cache add ca-certificates dcron

WORKDIR /root/

COPY --from=builder /app/slackpack .

RUN echo "0 2 * * * /root/slackpack >> /var/log/slackpack.log 2>&1" > /etc/crontabs/root

RUN touch /var/log/slackpack.log

CMD crond -f -d 8 && tail -f /var/log/slackpack.log
