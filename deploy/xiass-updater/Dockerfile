FROM alpine:3.20

RUN apk add --no-cache bash ca-certificates curl docker-cli docker-cli-compose git openssh-client

WORKDIR /usr/local/lib/xiass
COPY xiass-update.sh xiass-runtime-start.sh xiass-backup.sh ./
COPY xiass-updater-entrypoint.sh /usr/local/bin/xiass-updater
RUN chmod 0755 /usr/local/bin/xiass-updater ./xiass-update.sh ./xiass-runtime-start.sh ./xiass-backup.sh

ENTRYPOINT ["/usr/local/bin/xiass-updater"]
