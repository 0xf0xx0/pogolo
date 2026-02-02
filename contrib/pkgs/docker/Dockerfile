FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
ARG TARGETOS
ARG TARGETARCH

WORKDIR /build

# set envs for the rest of the owl
ENV GOOS=${TARGETOS}
ENV GOARCH=${TARGETARCH}
# flags stolen from arch linux requirements
ENV GOFLAGS="-buildmode=pie -trimpath -ldflags=-linkmode=external -mod=readonly -modcacherw"

COPY go.mod go.sum ./
RUN go mod download

COPY --exclude=contrib/ --exclude=mockdata/ --exclude=*_test.go ./ ./
RUN go build -ldflags="-s -w -buildid="

FROM alpine:latest

WORKDIR /

# MAYBE: copy docker pogolo.toml?
COPY --from=build /build/pogolo .

# stratum
EXPOSE 5661
# api
EXPOSE 5662

# expects a /config dir with a pogolo.toml
CMD ["/pogolo", "--conf", "/config/pogolo.toml"]
