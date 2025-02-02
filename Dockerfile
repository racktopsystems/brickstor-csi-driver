ARG BASE_IMAGE

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
# copy driver binary matching image architecture
ARG BIN_DIR VERSION TARGETOS TARGETARCH
ARG DRIVER_BIN=brickstor-csi-driver_${TARGETOS}_${TARGETARCH}_v${VERSION}
COPY ${BIN_DIR}/${DRIVER_BIN} /brickstor-csi-driver
RUN /brickstor-csi-driver --version
# init script: runs rpcbind before starting the plugin
RUN echo $'#!/usr/bin/env sh\nupdate-ca-certificates\nrpcbind;\n/brickstor-csi-driver "$@";\n' > /init.sh
RUN chmod +x /init.sh
ENTRYPOINT ["/init.sh"]
