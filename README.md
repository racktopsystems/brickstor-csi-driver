# BrickStor CSI Driver

BrickStor product page: [BrickStor SP](https://www.racktopsystems.com/brickstor-sp/).

This is a **development branch**, for the most recent stable version see "Supported versions".

## Overview
The BrickStor Container Storage Interface (CSI) Driver provides a CSI interface used by Container Orchestrators (CO) to manage the lifecycle of BrickStor datasets over NFS and SMB protocols.

## Supported kubernetes versions matrix

|                   | BrickStor 23.4+|
|-------------------|----------------|
| Kubernetes >=1.20 | 1.0.0 |

## Feature List
|Feature|Feature Status|CSI Driver Version|CSI Spec Version|Kubernetes Version|
|--- |--- |--- |--- |--- |
|Static Provisioning|Beta|>= v1.0.0|>= v1.0.0|>=1.13|
|Dynamic Provisioning|Beta|>= v1.0.0|>= v1.0.0|>=1.13|
|RW mode|Beta|>= v1.0.0|>= v1.0.0|>=1.13|
|RO mode|Beta|>= v1.0.0|>= v1.0.0|>=1.13|
|Creating and deleting snapshot|Beta|>= v1.0.0|>= v1.0.0|>=1.17|
|Provision volume from snapshot|Beta|>= v1.0.0|>= v1.0.0|>=1.17|
|Provision volume from another volume|Beta|>= v1.0.0|>= v1.0.0|>=1.17|
|List snapshots of a volume|Beta|>= v1.0.0|>= v1.0.0|>=1.17|
|Expand volume|Beta|>= v1.0.0|>= v1.1.0|>=1.16|
|Access list for volume (NFS only)|Beta|>= v1.0.0|>= v1.0.0|>=1.13|
|Topology|Beta|>= v1.0.0|>= v1.0.0|>=1.17|
|StorageClass Secrets|Beta|>= v1.0.0|>= v1.0.0|>=1.13|
|Mount options|Beta|>= v1.0.0|>= v1.0.0|>=v1.13|


## Requirements

- Kubernetes cluster must allow privileged pods, this flag must be set for the API server and the kubelet
  ([instructions](https://github.com/kubernetes-csi/docs/blob/735f1ef4adfcb157afce47c64d750b71012c8151/book/src/Setup.md#enable-privileged-pods)):
  ```
  --allow-privileged=true
  ```
- Require the API server and the kubelet feature gates
  ([instructions](https://github.com/kubernetes-csi/docs/blob/735f1ef4adfcb157afce47c64d750b71012c8151/book/src/Setup.md#enabling-features)):
  ```
  --feature-gates=VolumeSnapshotDataSource=true,VolumePVCDataSource=true,ExpandInUsePersistentVolumes=true,ExpandCSIVolumes=true,ExpandPersistentVolumes=true,Topology=true,CSINodeInfo=true
  ```
  If you are planning on using topology, the following feature-gates are required
  ```
  ServiceTopology=true,CSINodeInfo=true
  ```
- Mount propagation must be enabled, the Docker daemon for the cluster must allow shared mounts
  ([instructions](https://github.com/kubernetes-csi/docs/blob/735f1ef4adfcb157afce47c64d750b71012c8151/book/src/Setup.md#enabling-mount-propagation))
- Depending on preferred mount filesystem type, the following packages must be installed on each Kubernetes node:
  ```bash
  # for NFS
  apt install -y rpcbind nfs-common
  # for SMB
  apt install -y rpcbind cifs-utils
  ```

## Installation

1. Create the BrickStor default dataset for the driver; e.g. `p0/global/data/csi`.
   By default, the driver will create datasets under this dataset and mount them for use as Kubernetes volumes.
2. Clone the driver repository
   ```bash
   git clone https://github.com/racktopsystems/brickstor-csi-driver.git
   cd brickstor-csi-driver
   git checkout main (only if currently on a branch)
   ```
3. Edit the `deploy/kubernetes/brickstor-csi-driver-config.yaml` file. Driver configuration example:
   ```yaml
   brickstor_map:
     bsrcluster-east:
       restIp: https://10.2.21.12:8443,https://10.2.21.12:8443 # [required] BrickStor REST API endpoint(s)
       username: bsradmin                                      # [required] BrickStor REST API username
       password: p@ssword                                      # [required] BrickStor REST API password
       defaultDataset: p0/global/data/csi                      # default 'pool/dataset' to use
       defaultDataIp: 20.20.20.21                              # default BrickStor data IP or HA VIP
       defaultMountFsType: nfs                                 # default mount fs type [nfs|cifs]
       defaultMountOptions: noatime                            # default mount options (mount -o ...)
       zone: us-east                                           # zone to match kubernetes topology
     bsrcluster1:
       restIp: https://10.3.21.12:8443,https://10.3.21.12:8443 # [required] BrickStor REST API endpoint(s)
       username: bsradmin                                      # [required] BrickStor REST API username
       password: p@ssword                                      # [required] BrickStor REST API password
       defaultDataset: p03/global/data/csi                     # default 'pool/dataset' to use
       defaultDataIp: 10.10.10.21                              # default BrickStor data IP or HA VIP
       defaultMountFsType: nfs                                 # default mount fs type [nfs|cifs]
       defaultMountOptions: noatime                            # default mount options (mount -o ...)
     bsrcluster2:
       restIp: https://10.4.21.12:8443,https://10.4.21.13:8443 # [required] BrickStor REST API endpoint(s)
       username: bsradmin                                      # [required] BrickStor REST API username
       password: p@ssword                                      # [required] BrickStor REST API password
       defaultDataset: p01/global/data/csi                     # default 'pool/dataset' to use
       defaultDataIp: 11.11.22.33                              # default BrickStor data IP or HA VIP
       defaultMountFsType: nfs                                 # default mount fs type [nfs|cifs]
       defaultMountOptions: vers=4                             # default mount options (mount -o ...)


   # for CIFS mounts:
   #defaultMountFsType: cifs                               # default mount fs type [nfs|cifs]
   #defaultMountOptions: username=bsradmin,password=brickstor@1 # username/password must be defined for CIFS
   ```
   **Note**: keyword brickstor_map followed by node/cluster name of your choice MUST be used even if you are only using 1 BrickStor node or cluster.

   All driver configuration options:

   | Name                  | Description                                                     | Required   | Example                                                      |
   |-----------------------|-----------------------------------------------------------------|------------|--------------------------------------------------------------|
   | `restIp`              | BrickStor REST API endpoint(s); use `,` to separate cluster nodes | yes        | `https://10.2.21.10:8443`<br>`https://10.2.21.10:8443,https://10.2.21.11:8443`                                      |
   | `username`            | BrickStor REST API username                                   | yes        | `bsradmin`                                                      |
   | `password`            | BrickStor REST API password                                   | yes        | `p@ssword`                                                   |
   | `defaultDataset`      | parent dataset for driver's datasets [pool/dataset]          | no         | `p0/global/data/csi`                             |
   | `defaultDataIp`       | BrickStor data IP or HA VIP for mounting shares               | yes for PV | `20.20.20.21`                                                |
   | `defaultMountFsType`  | mount filesystem type [nfs, cifs](default: 'nfs')               | no         | `cifs`                                                       |
   | `defaultMountOptions` | NFS/CIFS mount options: `mount -o ...` (default: "")            | no         | NFS: `noatime,nosuid`<br>CIFS: `username=bsradmin,password=123` |
   | `debug`               | print more logs (default: false)                                | no         | `true`                                                       |
   | `zone`                | Zone to match topology.kubernetes.io/zone.                      | no         | `zone-1`                                                          |
   | `mountPointPermissions`| Permissions to be set on volume's mount point                | no            | `0777`     |
   | `insecureSkipVerify`| TLS certificates check will be skipped when `true` (default: 'true')| no            | `false`     |

   **Note**: if parameter `defaultDataset`/`defaultDataIp` is not specified in driver configuration,
   then parameter `dataset`/`dataIp` must be specified in _StorageClass_ configuration.

   **Note**: all default parameters (`default*`) may be overwritten in specific _StorageClass_ configuration.

   **Note**: if `defaultMountFsType` is set to `cifs` then parameter `defaultMountOptions` must include
   CIFS username and password (`username=bsradmin,password=123`).

4. Create Kubernetes secret from the file:
   ```bash
   kubectl create secret generic brickstor-csi-driver-config --from-file=deploy/kubernetes/brickstor-csi-driver-config.yaml
   ```
5. Register driver to Kubernetes:
   ```bash
   kubectl apply -f deploy/kubernetes/brickstor-csi-driver.yaml
   ```

BrickStor CSI driver's pods should be running after installation:

```bash
$ kubectl get pods
NAME                           READY   STATUS    RESTARTS   AGE
brickstor-csi-controller-0     3/3     Running   0          42s
brickstor-csi-node-cwp4v       2/2     Running   0          42s
```

## Usage

### Dynamically provisioned volumes

For dynamic volume provisioning, the administrator needs to set up a _StorageClass_ pointing to the driver.
In this case Kubernetes generates a volume name automatically (for example `pvc-ns-cfc67950-fe3c-11e8-a3ca-005056b857f8`).
Default driver configuration may be overwritten in `parameters` section:

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: brickstor-csi-driver-cs-nginx-dynamic
provisioner: brickstor-csi-driver.racktopsystems.com
mountOptions:                        # list of options for `mount -o ...` command
#  - noatime                         #
#- matchLabelExpressions:            # use to following lines to configure topology by zones
#  - key: topology.kubernetes.io/zone
#    values:
#    - us-east
parameters:
  #configName: bsr-ssd               # specify exact BrickStor appliance that you want to use to provision volumes.
  #dataset: customPool/customDataset # to overwrite "defaultDataset" config property [pool/dataset]
  #dataIp: 20.20.20.253              # to overwrite "defaultDataIp" config property
  #mountFsType: nfs                  # to overwrite "defaultMountFsType" config property
  #mountOptions: noatime             # to overwrite "defaultMountOptions" config property
  #nfsAccessList: rw:10.3.196.93, ro:2.2.2.2, 3.3.3.3/10   # optional list to manage access by fqdn.

```

#### Parameters

| Name           | Description                                            | Example                                               |
|----------------|--------------------------------------------------------|-------------------------------------------------------|
| `dataset`      | parent dataset for driver's filesystems [pool/dataset] | `customPool/customDataset`                            |
| `dataIp`       | BrickStor data IP or HA VIP for mounting shares      | `20.20.20.253`                                        |
| `mountFsType`  | mount filesystem type [nfs, cifs](default: 'nfs')      | `cifs`                                                |
| `mountOptions` | NFS/CIFS mount options: `mount -o ...`                 | NFS: `noatime`<br>CIFS: `username=bsradmin,password=123` |
| `configName`   | name of BrickStor node or cluster from config file | `bsr-ssd`                                        |
| `nfsAccessList`| List of addresses to allow NFS access to. Format: `[accessMode]:[address]/[mask]`. `accessMode` and `mask` are optional, default mode is `rw`.| rw:10.3.196.93, ro:2.2.2.2, 3.3.3.3/10 |


### Pre-provisioned volumes

The driver can use pre-existing BrickStor datasets,
in this case, _StorageClass_, _PersistentVolume_ and _PersistentVolumeClaim_ should be configured.

#### _StorageClass_ configuration

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: brickstor-csi-driver-cs-nginx-persistent
provisioner: brickstor-csi-driver.racktopsystems.com
mountOptions:                        # list of options for `mount -o ...` command
#  - noatime                         #
parameters:
  #dataset: customPool/customDataset # to overwrite "defaultDataset" config property [pool/dataset]
  #dataIp: 20.20.20.253              # to overwrite "defaultDataIp" config property
  #mountFsType: nfs                  # to overwrite "defaultMountFsType" config property
  #mountOptions: noatime             # to overwrite "defaultMountOptions" config property
```

#### _PersistentVolume_ configuration

```yaml
apiVersion: v1
kind: PersistentVolume
metadata:
  name: brickstor-csi-driver-pv-nginx-persistent
  labels:
    name: brickstor-csi-driver-pv-nginx-persistent
spec:
  storageClassName: brickstor-csi-driver-cs-nginx-persistent
  accessModes:
    - ReadWriteMany
  capacity:
    storage: 1Gi
  csi:
    driver: brickstor-csi-driver.racktopsystems.com
    volumeHandle: bsr-ssd:p0/global/data/csi/nginx-persistent
  #mountOptions:  # list of options for `mount` command
  #  - noatime    #
```

CSI Parameters:

| Name           | Description                                                       | Example                              |
|----------------|-------------------------------------------------------------------|--------------------------------------|
| `driver`       | installed driver name "brickstor-csi-driver.racktopsystems.com"        | `brickstor-csi-driver.racktopsystems.com` |
| `volumeHandle` | appliance name from config and path to existing BrickStor dataset [configName:pool/datasetA/datasetB] | `bsr-ssd:p0/global/datasetA/nginx`               |

#### _PersistentVolumeClaim_ (pointed to created _PersistentVolume_)

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: brickstor-csi-driver-pvc-nginx-persistent
spec:
  storageClassName: brickstor-csi-driver-cs-nginx-persistent
  accessModes:
    - ReadWriteMany
  resources:
    requests:
      storage: 1Gi
  selector:
    matchLabels:
      # to create 1-1 relationship for pod - persistent volume use unique labels
      name: brickstor-csi-driver-pv-nginx-persistent
```

#### Example

Run nginx server using PersistentVolume.

**Note:** Pre-configured dataset should exist on the BrickStor:
`p0/global/data/csi/nginx-persistent`.

```bash
kubectl apply -f examples/kubernetes/nginx-persistent-volume.yaml

# to delete this pod:
kubectl delete -f examples/kubernetes/nginx-persistent-volume.yaml
```

### Cloned volumes

We can create a clone of an existing csi volume.
To do so, we need to create a _PersistentVolumeClaim_ with _dataSource_ spec pointing to an existing PVC that we want to clone.
In this case Kubernetes generates a volume name automatically (for example `pvc-ns-cfc67950-fe3c-11e8-a3ca-005056b857f8`).

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: brickstor-csi-driver-pvc-nginx-dynamic-clone
spec:
  storageClassName: brickstor-csi-driver-cs-nginx-dynamic
  dataSource:
    kind: PersistentVolumeClaim
    apiGroup: ""
    name: brickstor-csi-driver-pvc-nginx-dynamic # pvc name
  accessModes:
    - ReadWriteMany
  resources:
    requests:
      storage: 1Gi
```

#### Example

Run Nginx pod with dynamically provisioned volume:

```bash
kubectl apply -f examples/kubernetes/nginx-clone-volume.yaml

# to delete this pod:
kubectl delete -f examples/kubernetes/nginx-clone-volume.yaml
```

## Snapshots

```bash
# create snapshot class
kubectl apply -f examples/kubernetes/snapshot-class.yaml

# take a snapshot
kubectl apply -f examples/kubernetes/take-snapshot.yaml

# deploy nginx pod with volume restored from a snapshot
kubectl apply -f examples/kubernetes/nginx-snapshot-volume.yaml

# snapshot classes
kubectl get volumesnapshotclasses.snapshot.storage.k8s.io

# snapshot list
kubectl get volumesnapshots.snapshot.storage.k8s.io

# snapshot content list
kubectl get volumesnapshotcontents.snapshot.storage.k8s.io
```

## Checking TLS cecrtificates
Default driver behavior is to skip certificate checks for all Rest API calls.
To manage this, use the config parameter `insecureSkipVerify`=<true>.
When `InsecureSkipVerify` is set to false, the driver will enforce certificate checking.
To allow adding certificates, brickstor-csi-driver.yaml has additional volumes added to brickstor-csi-controller deployment and brickstor-csi-node daemonset.
```bash
            - name: certs-dir
              mountPropagation: HostToContainer
              mountPath: /usr/local/share/ca-certificates
        - name: certs-dir
          hostPath:
            path: /etc/ssl/  # change this to your tls certificates folder
            type: Directory
```
`/etc/ssl` folder is the default certificates location for Ubuntu. Change this according to your
OS configuration.
If you only want to propagate a specific set of certificates instead of the whole cert folder
from the host, you can put them in any folder on the host and set in the yaml file accordingly.
Note that this should be done on every node of the kubernetes cluster.

## Uninstall

Using the same files as for installation:

```bash
# delete driver
kubectl delete -f deploy/kubernetes/brickstor-csi-driver.yaml

# delete secret
kubectl delete secret brickstor-csi-driver-config
```

## Troubleshooting

- Show installed drivers:
  ```bash
  kubectl get csidrivers
  kubectl describe csidrivers
  ```
- Error:
  ```
  MountVolume.MountDevice failed for volume "pvc-ns-<...>" :
  driver name brickstor-csi-driver.racktopsystems.com not found in the list of registered CSI drivers
  ```
  Make sure _kubelet_ configured with `--root-dir=/var/lib/kubelet`, otherwise update paths in the driver yaml file
  ([all requirements](https://github.com/kubernetes-csi/docs/blob/387dce893e59c1fcf3f4192cbea254440b6f0f07/book/src/Setup.md#enabling-features)).
- "VolumeSnapshotDataSource" feature gate is disabled:
  ```bash
  vim /var/lib/kubelet/config.yaml
  # ```
  # featureGates:
  #   VolumeSnapshotDataSource: true
  # ```
  vim /etc/kubernetes/manifests/kube-apiserver.yaml
  # ```
  #     - --feature-gates=VolumeSnapshotDataSource=true
  # ```
  ```
- Driver logs
  ```bash
  kubectl logs -f brickstor-csi-controller-0 driver
  kubectl logs -f $(kubectl get pods | awk '/brickstor-csi-node/ {print $1;exit}') driver
  ```
- Show termination message in case driver failed to run:
  ```bash
  kubectl get pod brickstor-csi-controller-0 -o go-template="{{range .status.containerStatuses}}{{.lastState.terminated.message}}{{end}}"
  ```
- Configure Docker to trust insecure registries:
  ```bash
  # add `{"insecure-registries":["10.3.199.92:5000"]}` to:
  vim /etc/docker/daemon.json
  service docker restart
  ```

## Development

Commits should follow [Conventional Commits Spec](https://conventionalcommits.org).

### Build

```bash
# print variables and help
make

# build go app on local machine
make build
```

### Run

Without installation to k8s cluster only version command works:

```bash
./bin/brickstor-csi-driver --version
```

### Publish

```bash
# push the latest built container to the local registry (specify the correct REGISTRY address)
make container-build PUSH=1 REGISTRY=192.168.1.1:5000
```

### Tests

CSI sanity tests from https://github.com/kubernetes-csi/csi-test. Update `tests/csi-sanity/driver-config-csi-sanity.yaml` with the local BrickStor configuration to use for the test.

```bash
make test-csi-sanity-container
```

End-to-end driver tests with real K8s and BrickStor appliances. See [Makefile](Makefile) for more examples.

```bash
# Test options to be set before run tests:
# - NOCOLORS=true            # to run w/o colors
# - TEST_K8S_IP=10.3.199.250 # e2e k8s tests

# run all tests using local registry (`REGISTRY_LOCAL` in `Makefile`)
TEST_K8S_IP=10.3.199.250 make test-all-local-image
# run all tests using hub.docker.com registry (`REGISTRY` in `Makefile`)
TEST_K8S_IP=10.3.199.250 make test-all-remote-image

# run tests in container:
# - RSA keys from host's ~/.ssh directory will be used by container.
#   Make sure all remote hosts used in tests have host's RSA key added as trusted
#   (ssh-copy-id -i ~/.ssh/id_rsa.pub user@host)
#
# run all tests using local registry (`REGISTRY_LOCAL` in `Makefile`)
TEST_K8S_IP=10.3.199.250 make test-all-local-image-container
# run all tests using hub.docker.com registry (`REGISTRY` in `Makefile`)
TEST_K8S_IP=10.3.199.250 make test-all-remote-image-container
```

End-to-end K8s test parameters:

```bash
# Tests install driver to k8s and run nginx pod with mounted volume
# "export NOCOLORS=true" to run w/o colors
go test tests/e2e/driver_test.go -v -count 1 \
    --k8sConnectionString="root@10.3.199.250" \
    --k8sDeploymentFile="../../deploy/kubernetes/brickstor-csi-driver.yaml" \
    --k8sSecretFile="./_configs/driver-config-single-default.yaml"
```

All development happens in the `main` branch,
when it's time to publish a new version, a new git tag should be created.

1. Build and test the new version using local registry:
   ```bash
   # build development version:
   make container-build
   # publish to local registry
   make container-build PUSH=1 REGISTRY=192.168.1.1:5000
   # test plugin using local registry
   TEST_K8S_IP=10.3.199.250 make test-all-local-image-container
   ```

2. To release a new version run command:
   ```bash
   make release VERSION=X.X.X LATEST=1
   ```
   This script does following:
   - builds driver container 'brickstor-csi-driver'
   - Login to hub.docker.com will be requested
   - publishes driver version 'racktopsystems/brickstor-csi-driver:X.X.X' to hub.docker.com
   - creates new Git tag 'vX.X.X' and pushes to the repository.

3. Update Github [releases](https://github.com/racktopsystems/brickstor-csi-driver/releases).
