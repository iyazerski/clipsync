# Build from the repo root: docker build -f relay/Dockerfile .
FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
COPY internal/protocol ./internal/protocol
COPY relay ./relay
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -mod=readonly -trimpath -ldflags='-s -w' -o /clipsync-relay ./relay

FROM scratch
COPY --from=build /clipsync-relay /clipsync-relay
ENV BIND_ADDR=0.0.0.0:8080
EXPOSE 8080
USER 10001:10001
ENTRYPOINT ["/clipsync-relay"]
