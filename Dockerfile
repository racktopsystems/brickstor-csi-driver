ARG BASE_IMAGE
ARG BUILD_IMAGE

# build container
FROM ${BUILD_IMAGE:-golang:latest} AS builder
WORKDIR /go/src/github.com/racktopsystems/brickstor-csi-driver/
COPY . ./
ARG VERSION
ENV VERSION=${VERSION:-0.0.0}
ARG TARGETOS TARGETARCH
ARG DRIVER_BIN=brickstor-csi-driver_${TARGETOS}_${TARGETARCH}
RUN go version
RUN make build
RUN cp bin/${DRIVER_BIN} /brickstor-csi-driver

# driver container
FROM ${BASE_IMAGE:-alpine:latest}
LABEL name="brickstor-csi-driver"
LABEL maintainer="RackTop Systems, Inc."
LABEL description="BrickStor CSI Driver"
LABEL io.k8s.description="BrickStor CSI Driver"
# install nfs and smb dependencies
RUN apk add --no-cache rpcbind nfs-utils cifs-utils ca-certificates
RUN apk update && apk add "libcrypto3>=3.0.8-r3" "libssl3>=3.0.8-r3" && rm -rf /var/cache/apt/*
# create driver config folder and print version
RUN mkdir -p /config/
COPY --from=builder /brickstor-csi-driver /
RUN /brickstor-csi-driver --version
# init script: runs rpcbind before starting the plugin
RUN echo $'#!/usr/bin/env sh\nupdate-ca-certificates\nrpcbind;\n/brickstor-csi-driver "$@";\n' > /init.sh
RUN chmod +x /init.sh
ENTRYPOINT ["/init.sh"]
