FROM alpine:3.14
RUN apk add --no-cache tzdata ca-certificates

COPY main main
COPY configs configs
