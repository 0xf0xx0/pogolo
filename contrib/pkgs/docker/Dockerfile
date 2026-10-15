FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:f92b6ef800e499660581efdabdf25d9d817a9d124eaf900924f0504e7e27e12d AS build
ARG TARGETOS
ARG TARGETARCH

WORKDIR /build

# set envs for the rest of the owl
ARG GOOS=$TARGETOS
ARG GOARCH=$TARGETARCH
ARG GOFLAGS="-buildmode=pie -trimpath -ldflags=-linkmode=external -mod=readonly -modcacherw"

COPY go.mod go.sum ./
RUN go mod download

COPY --exclude=contrib/ --exclude=mockdata/ --exclude=imgs/ --exclude=*_test.go ./ ./
RUN go build -ldflags="-s -w -buildid="

FROM alpine:3.24@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6

WORKDIR /

RUN apk add --no-cache libc6-compat
COPY --from=build /build/pogolo .

# stratum
EXPOSE 5661
# api
EXPOSE 5662

# expects a /config dir with a pogolo.toml
CMD ["/pogolo", "--conf", "/config/pogolo.toml"]
