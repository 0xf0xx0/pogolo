FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS build
ARG TARGETOS
ARG TARGETARCH

WORKDIR /build

# set envs for the rest of the owl
ARG GOOS=$TARGETOS
ARG GOARCH=$TARGETARCH
ARG GOFLAGS="-buildmode=pie -trimpath -ldflags=-linkmode=external -mod=readonly -modcacherw"

COPY go.mod go.sum ./
RUN go mod download

COPY --exclude=contrib/ --exclude=go.mod --exclude=go.sum --exclude=mockdata/ --exclude=imgs/ --exclude=*_test.go ./ ./
RUN go build -ldflags="-s -w -buildid="

FROM alpine:3.24@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf

WORKDIR /

RUN apk add --no-cache libc6-compat
COPY --from=build /build/pogolo .

# stratum
EXPOSE 5661
# api
EXPOSE 5662

# expects a /config dir with a pogolo.toml
CMD ["/pogolo", "--conf", "/config/pogolo.toml"]
