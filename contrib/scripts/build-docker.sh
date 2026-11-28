#!/bin/bash
set -eu

# TODO
upload="--output type=registry"

case $1 in
"testing")
    docker buildx build --platform linux/amd64,linux/arm64 \
        -f contrib/pkgs/docker/Dockerfile --tag 0xf0xx0/pogolo:testing \
        $upload .
;;
"release")
    tag="$(git describe --tags --abbrev=0)"
    docker buildx build --platform linux/arm64,linux/amd64 \
        -f contrib/pkgs/docker/Dockerfile --tag "0xf0xx0/pogolo:$tag" \
        --tag 0xf0xx0/pogolo:latest --tag 0xf0xx0/pogolo:v1.x $upload .
;;
 *)
     echo "either 'release' or 'testing'"
     exit 1
esac
