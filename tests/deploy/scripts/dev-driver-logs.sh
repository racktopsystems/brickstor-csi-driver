#!/usr/bin/env bash

echo
echo -e "\n------------- CONTROLLER -------------\n"
echo
kubectl logs --all-containers=true brickstor-csi-controller-0;

echo
echo -e "\n------------- NODE -------------\n"
echo
kubectl logs --all-containers=true $(kubectl get pods | awk '/brickstor-csi-node/ {print $1;exit}');
