# github2telegram
Bot that can send notification of new releases to Telegram

Status
------

Alpha quality. Needs major rework. Compiles. Works. Might contain security bugs

## Quick Start with Docker

This package is available for Docker:

```console
docker run -d --hostname github2telegram --name github2telegram -v $(pwd)/config.yaml:/app/config.yaml wwwlde/github2telegram
```

### Building own Docker image

```console
docker run -it --rm alpine /bin/sh -c "apk add --no-cache ca-certificates 2>&1 > /dev/null && cat /etc/ssl/certs/ca-certificates.crt" > ca-certificates.crt
docker build -t wwwlde/github2telegram --no-cache --force-rm .
```
