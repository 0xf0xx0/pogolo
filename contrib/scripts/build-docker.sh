#!/bin/bash
set -eu

case $1 in
"testing")
    docker buildx build --no-cache --platform linux/amd64,linux/arm64 \
        -f contrib/pkgs/docker/Dockerfile --tag 0xf0xx0/pogolo:testing \
        .
;;
"release")
    tag="$(git describe --tags --abbrev=0)"
    docker buildx build --platform linux/arm64,linux/amd64 \
        -f contrib/pkgs/docker/Dockerfile --tag "0xf0xx0/pogolo:$tag" \
        --tag 0xf0xx0/pogolo:latest --tag 0xf0xx0/pogolo:v1.x --output "type=registry" .
;;
 *)
     echo "either 'release' or 'testing'"
     exit 1
esac
