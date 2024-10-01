#!/usr/bin/env bash

#
# Combine all driver logs into single stdout
#

{ \
    kubectl logs -f --all-containers=true brickstor-csi-controller-0 & \
    kubectl logs -f --all-containers=true $(kubectl get pods | awk '/brickstor-csi-node/ {print $1;exit}'); \
}
