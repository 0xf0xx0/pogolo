FROM --platform=$BUILDPLATFORM golang:1.25.5-alpine AS build
ARG TARGETOS
ARG TARGETARCH

WORKDIR /build

COPY ./ ./
ENV GOOS=${TARGETOS}
ENV GOARCH=${TARGETARCH}
ENV GOFLAGS="-buildmode=pie -trimpath -ldflags=-linkmode=external -mod=readonly -modcacherw"
RUN go mod download
RUN go build -ldflags="-s -w -buildid="

FROM golang:1.25.5-alpine

WORKDIR /

COPY --from=build /build .

# stratum
EXPOSE 5661
# api
EXPOSE 5662

# expects a /config dir with a pogolo.toml
CMD ["./pogolo", "--conf", "/config/pogolo.toml"]
