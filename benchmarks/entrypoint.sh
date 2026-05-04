#!/bin/sh
set -eu

mkdir -p /data
chown -R 65532:65534 /data

exec setpriv --reuid=65532 --regid=65534 --clear-groups /app/lumenvec "$@"
