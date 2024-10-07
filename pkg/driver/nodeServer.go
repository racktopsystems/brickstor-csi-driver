//TODO Consider to add NodeStageVolume() method:
// - called by k8s to temporarily mount the volume to a staging path
// - staging path is a global directory on the node
// - k8s allows user to use a single volume by multiple pods (for NFS)
// - if all pods run on the same node the single mount point will be used by all of them.

package driver

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/kubernetes-csi/csi-lib-utils/protosanitizer"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/mount-utils"

	"github.com/racktopsystems/brickstor-csi-driver/pkg/bsr"
	"github.com/racktopsystems/brickstor-csi-driver/pkg/config"
)

// mount options regexps
var regexpMountOptionRo = regexp.MustCompile("^ro$")
var regexpMountOptionVers = regexp.MustCompile("^vers=.*$")
var regexpMountOptionTimeo = regexp.MustCompile("^timeo=.*$")
var regexpMountOptionUsername = regexp.MustCompile("^username=.+$")
var regexpMountOptionPassword = regexp.MustCompile("^password=.+$")
var regexpMountOptionNolock = regexp.MustCompile("^nolock.+$")

const DefaultMountPointPermissions = 0777

// NodeServer - k8s csi driver node server
type NodeServer struct {
	nodeID         string
	bsrResolverMap map[string]*bsr.Resolver
	mutex          sync.Mutex // protects bsrResolverMap
	config         *config.Config
	log            *logrus.Entry

	csi.UnimplementedNodeServer
}

func (s *NodeServer) refreshConfig(secret string) error {
	changed, err := s.config.Refresh(secret)
	if err != nil {
		return err
	}

	if changed {
		s.log.Info("config has been changed, updating...")

		resolverMap, err := newResolverMap(s.config, s.log)
		if err != nil {
			return err
		}

		s.mutex.Lock()
		s.bsrResolverMap = resolverMap
		s.mutex.Unlock()
	}

	return nil
}

func (s *NodeServer) resolveBSR(configName, datasetPath string) (bsr.ProviderInterface, error, string) {

	l := s.log.WithField("func", "resolveBSR()")
	l.Infof("configName: %s, datasetPath: %s", configName, datasetPath)

	s.mutex.Lock()
	resolver := s.bsrResolverMap[configName]
	s.mutex.Unlock()

	bsrProvider, err := resolver.Resolve(datasetPath)
	if err != nil {
		code := codes.Internal
		if bsr.ErrIsNotExist(err) {
			code = codes.NotFound
		}
		return nil, status.Errorf(code,
			"Cannot resolve '%s' on any BrickStor: %s", datasetPath, err), ""
	}

	return bsrProvider, nil, configName
}

// GetMountPointPermissions - check if mountPoint persmissions were set in config or use default
func (s *NodeServer) GetMountPointPermissions(volumeContext map[string]string) (os.FileMode, error) {
	l := s.log.WithField("func", "GetMountPointPermissions()")
	l.Infof("volumeContext: '%+v'", volumeContext)
	mountPointPermissions := volumeContext["mountPointPermissions"]
	if mountPointPermissions == "" {
		l.Infof("mountPointPermissions is not set, using default: '%+v'", strconv.FormatInt(
			int64(DefaultMountPointPermissions), 8))
		return os.FileMode(DefaultMountPointPermissions), nil
	}
	octalPerm, err := strconv.ParseInt(mountPointPermissions, 8, 16)
	if err != nil {
		return 0, err
	}
	return os.FileMode(octalPerm), nil
}

// NodeGetInfo - get node info
func (s *NodeServer) NodeGetInfo(ctx context.Context, req *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	s.log.WithField("func", "NodeGetInfo()").Infof("request: '%+v'", req)

	return &csi.NodeGetInfoResponse{
		NodeId: s.nodeID,
		AccessibleTopology: &csi.Topology{
			Segments: map[string]string{},
		},
	}, nil
}

// NodeGetCapabilities - get node capabilities
func (s *NodeServer) NodeGetCapabilities(ctx context.Context, req *csi.NodeGetCapabilitiesRequest) (
	*csi.NodeGetCapabilitiesResponse,
	error,
) {
	s.log.WithField("func", "NodeGetCapabilities()").Infof("request: '%+v'", req)

	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: []*csi.NodeServiceCapability{
			// TODO re-enable the capability when NodeGetVolumeStats()
			// validates volume path.
			// {
			//  Type: &csi.NodeServiceCapability_Rpc{
			//      Rpc: &csi.NodeServiceCapability_RPC{
			//          Type: csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
			//      },
			//  },
			// },
		},
	}, nil
}

// NodePublishVolume - mounts BrickStor dataset to the node
func (s *NodeServer) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (
	*csi.NodePublishVolumeResponse,
	error,
) {
	l := s.log.WithField("func", "NodePublishVolume()")
	l.Infof("request: '%+v'", protosanitizer.StripSecrets(req))

	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "req.VolumeId must be provided")
	}

	targetPath := req.GetTargetPath()
	if len(targetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "req.TargetPath must be provided")
	}

	//TODO validate VolumeCapability
	volumeCapability := req.GetVolumeCapability()
	if volumeCapability == nil {
		return nil, status.Error(codes.InvalidArgument, "req.VolumeCapability must be provided")
	}
	var secret string
	secrets := req.GetSecrets()
	for _, v := range secrets {
		secret = v
	}
	// read and validate config
	err := s.refreshConfig(secret)
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "Cannot use config file: %s", err)
	}

	volInfo, err := ParseVolumeID(volumeID)
	if err != nil {
		l.Errorf("Got wrong volumeId, VolumeInfo error: %s", err)
		return nil, status.Error(codes.NotFound, fmt.Sprintf("VolumeId is in wrong format: %s", volumeID))
	}

	configName := volInfo.ConfigName
	volumePath := volInfo.Path

	bsrProvider, err, configName := s.resolveBSR(configName, volumePath)
	if err != nil {
		return nil, err
	}

	cfg, ok := s.config.LookupNode(configName)
	if !ok {
		l.Warnf("Missing node %s", configName)
		return nil, status.Error(codes.NotFound,
			fmt.Sprintf("Node %s was removed", configName))
	}
	l.Infof("resolved BrickStor: %s, %+v", bsrProvider.Addr(), volumePath)

	// get BrickStor filesystem information
	dataset, err := bsrProvider.GetDataset(volumePath)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "Cannot find filesystem '%s': %s", volumePath, err)
	}

	// volume attributes are passed from ControllerServer.CreateVolume()
	volumeContext := req.GetVolumeContext()
	if volumeContext == nil {
		volumeContext = make(map[string]string)
	}

	// get mount options by this priority (takes first one found):
	//  - k8s runtime volume mount options:
	//      - `k8s.PersistentVolume.spec.mountOptions` definition
	//      - `k8s.StorageClass.mountOptions` (works in k8s >=v1.13)
	//  - runtime volume attributes: `k8s.StorageClass.parameters.mountOptions`
	//  - driver config file (k8s secret): `defaultMountOptions`
	mountOptions := volumeCapability.GetMount().GetMountFlags()
	if mountOptions == nil {
		mountOptions = []string{}
	}
	if len(mountOptions) == 0 {
		var configMountOptions string
		if v, ok := volumeContext["mountOptions"]; ok && v != "" {
			// `k8s.StorageClass.parameters` in volume definition
			configMountOptions = v
		} else {
			// `defaultMountOptions` in driver config file
			configMountOptions = cfg.DefaultMountOptions
		}
		for _, option := range strings.Split(configMountOptions, ",") {
			if option != "" {
				mountOptions = append(mountOptions, option)
			}
		}
	}

	// add "ro" mount option if k8s requests it
	if req.GetReadonly() {
		//TODO use https://github.com/kubernetes/kubernetes/blob/master/pkg/volume/util/util.go#L759 ?
		mountOptions = appendMissing(mountOptions, regexpMountOptionRo, "ro")
	}

	// get dataIP checking by priority:
	//  - runtime volume attributes: `k8s.StorageClass.parameters.dataIP`
	//  - driver config file (k8s secret): `defaultDataIP`
	var dataIP string
	if v, ok := volumeContext["dataIP"]; ok && v != "" {
		dataIP = v
	} else {
		dataIP = cfg.DefaultDataIP
	}

	// get mount filesystem type checking by priority:
	//  - runtime volume attributes: `k8s.StorageClass.parameters.mountFsType`
	//  - driver config file (k8s secret): `defaultMountFsType`
	//  - fallback to NFS as default mount filesystem type
	var fsType string
	if v, ok := volumeContext["mountFsType"]; ok && v != "" {
		fsType = v
	} else if cfg.DefaultMountFsType != "" {
		fsType = cfg.DefaultMountFsType
	} else {
		fsType = config.FsTypeNFS
	}

	// share and mount filesystem with selected type
	if fsType == config.FsTypeNFS {
		err = s.mountNFS(req, bsrProvider, dataset, dataIP, mountOptions)
	} else if fsType == config.FsTypeCIFS {
		err = s.mountCIFS(req, bsrProvider, dataset, dataIP, mountOptions)
	} else {
		err = status.Errorf(codes.FailedPrecondition, "Unsupported mount filesystem type: '%s'", fsType)
	}
	if err != nil {
		if strings.Contains(err.Error(), "already a mount point") {
			l.Warnf("Target path '%s' is already a mount point", targetPath)
		} else {
			return nil, err
		}
	}

	permissions, err := s.GetMountPointPermissions(volumeContext)
	if err != nil {
		return nil, err
	}

	// Set write permissions if not read-only
	if !containsString(mountOptions, "ro") {
		l.Infof("Setting mount point permissions to %+v", permissions)
		err = os.Chmod(targetPath, permissions)
		if err != nil {
			if !strings.Contains(err.Error(), "read-only") {
				return nil, err
			}
		}
	}

	l.Infof("volume '%s' has been published to '%s'", volumeID, targetPath)
	return &csi.NodePublishVolumeResponse{}, nil
}

func (s *NodeServer) mountNFS(req *csi.NodePublishVolumeRequest,
	bsrProvider bsr.ProviderInterface, dataset bsr.Dataset, dataIP string,
	mountOptions []string) error {

	// create NFS share if it doesn't exist
	if !dataset.SharedOverNfs {
		err := bsrProvider.CreateNfsShare(dataset.Path, "")
		if err != nil {
			return status.Errorf(codes.Internal, "Cannot share dataset '%s' over NFS: %s", dataset.Path, err)
		}

		// applied for new datasets only, not pre-existing shared datasets
		err = bsrProvider.SetDatasetACL(dataset.Path, req.GetReadonly())
		if err != nil {
			return status.Errorf(codes.Internal, "Cannot set dataset ACL for '%s': %s", dataset.Path, err)
		}
	}

	// NFS style mount source
	mountSource := fmt.Sprintf("%s:%s", dataIP, dataset.MountPoint)

	// NFS v3 is used by default if no version specified by user
	mountOptions = appendMissing(mountOptions, regexpMountOptionVers, "vers=3")

	// If NFS v3 use nolock option
	if containsString(mountOptions, "vers=3") {
		mountOptions = appendMissing(mountOptions, regexpMountOptionNolock, "nolock")
	}

	// NFS option `timeo=100` is used by default if not specified by user
	mountOptions = appendMissing(mountOptions, regexpMountOptionTimeo, "timeo=100")

	return s.doMount(mountSource, req.GetTargetPath(), config.FsTypeNFS, mountOptions)
}

func (s *NodeServer) mountCIFS(req *csi.NodePublishVolumeRequest,
	bsrProvider bsr.ProviderInterface, dataset bsr.Dataset, dataIP string,
	mountOptions []string) error {

	// validate CIFS mount options
	for _, optionRE := range []*regexp.Regexp{regexpMountOptionUsername, regexpMountOptionPassword} {
		if !containsRE(mountOptions, optionRE) {
			return status.Errorf(
				codes.FailedPrecondition,
				"Options '%s' must be specified for CIFS mount (got options: %v)",
				optionRE,
				mountOptions,
			)
		}
	}

	// create SMB share if not exists
	if !dataset.SharedOverSmb {
		err := bsrProvider.CreateSmbShare(dataset.Path, dataset.GetDefaultSmbShareName())
		if err != nil {
			return status.Errorf(codes.Internal, "Cannot share filesystem '%s' over SMB: %s", dataset.Path, err)
		}

		// applied for new datasets only, not pre-existing shared datasets
		err = bsrProvider.SetDatasetACL(dataset.Path, req.GetReadonly())
		if err != nil {
			return status.Errorf(codes.Internal, "cannot set dataset ACL for '%s': %s", dataset.Path, err)
		}
	}

	// get smb share name
	shareName, err := bsrProvider.GetSmbShareName(dataset.Path)
	if err != nil {
		return err
	}

	// CIFS style mount source
	mountSource := fmt.Sprintf("//%s/%s", dataIP, shareName)

	return s.doMount(mountSource, req.GetTargetPath(), config.FsTypeCIFS, mountOptions)
}

func (s *NodeServer) doMount(mountSource, targetPath, fsType string,
	mountOptions []string) error {

	l := s.log.WithField("func", "doMount()")
	mounter := mount.New("")

	// check if mountpoint exists, create if there is no such directory
	notMountPoint, err := mounter.IsLikelyNotMountPoint(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			if err := os.MkdirAll(targetPath, 0750); err != nil {
				return status.Errorf(codes.Internal,
					"Failed to mkdir to share target path '%s': %s",
					targetPath, err)
			}
			notMountPoint = true
		} else {
			return status.Errorf(codes.Internal,
				"Cannot ensure that target path '%s' can be used as a mount point: %s",
				targetPath, err)
		}
	}

	if !notMountPoint { // already mounted
		return status.Errorf(codes.Internal, "Target path '%s' is already a mount point", targetPath)
	}

	l.Infof("mount params: type: '%s', mountSource: '%s', targetPath: '%s', mountOptions(%v): %+v",
		fsType, mountSource, targetPath, len(mountOptions), mountOptions)

	// If the dataset was just shared, we want to be tolerant if sharemgr
	// is a little slow getting setup.
	i := 0
	for ; i < 10; i++ {
		err = mounter.Mount(mountSource, targetPath, fsType, mountOptions)
		// if err != nil {
		if err == nil {
			break
		}

		if os.IsPermission(err) {
			return status.Errorf(codes.PermissionDenied,
				"Permission denied to mount '%s' to '%s': %s",
				mountSource, targetPath, err)

		} else if strings.Contains(err.Error(), "invalid argument") {
			return status.Errorf(codes.InvalidArgument,
				"Cannot mount '%s' to '%s', invalid argument: %s",
				mountSource, targetPath, err)
		}

		time.Sleep(time.Second)
	}

	if i == 10 {
		return status.Errorf(codes.Internal, "Failed to mount '%s' to '%s': %s",
			mountSource, targetPath, err)
	}

	return nil
}

// NodeUnpublishVolume - umount BrickStor dataset from the node and delete
// directory if successful.
func (s *NodeServer) NodeUnpublishVolume(ctx context.Context,
	req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {

	l := s.log.WithField("func", "NodeUnpublishVolume()")
	l.Infof("request: '%+v'", protosanitizer.StripSecrets(req))

	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID must be provided")
	}

	targetPath := req.GetTargetPath()
	if len(targetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path must be provided")
	}

	mounter := mount.New("")

	notMountPoint, err := mounter.IsLikelyNotMountPoint(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			l.Warnf("mount point '%s' already doesn't exist: '%s', return OK", targetPath, err)
			return &csi.NodeUnpublishVolumeResponse{}, nil
		}
		return nil, status.Errorf(
			codes.Internal,
			"Cannot ensure that target path '%s' is a mount point: '%s'",
			targetPath,
			err,
		)
	}

	if notMountPoint {
		if err := os.Remove(targetPath); err != nil {
			l.Infof("Remove target path error: %s", err.Error())
		}
		return &csi.NodeUnpublishVolumeResponse{}, nil
	}

	if err := mounter.Unmount(targetPath); err != nil {
		return nil, status.Errorf(codes.Internal, "Failed to unmount target path '%s': %s", targetPath, err)
	}

	if err := os.Remove(targetPath); err != nil && !os.IsNotExist(err) {
		return nil, status.Errorf(codes.Internal, "Cannot remove unmounted target path '%s': %s", targetPath, err)
	}

	l.Infof("volume '%s' has been unpublished from '%s'", volumeID, targetPath)
	return &csi.NodeUnpublishVolumeResponse{}, nil
}

// NodeGetVolumeStats - volume stats (available capacity)
// TODO https://github.com/container-storage-interface/spec/blob/master/spec.md#nodegetvolumestats
func (s *NodeServer) NodeGetVolumeStats(ctx context.Context, req *csi.NodeGetVolumeStatsRequest) (
	*csi.NodeGetVolumeStatsResponse,
	error,
) {
	l := s.log.WithField("func", "NodeGetVolumeStats()")
	l.Infof("request: '%+v'", protosanitizer.StripSecrets(req))

	// volumePath MUST be an absolute path in the root filesystem of the
	// process serving this request.
	// TODO validate volumePath then re-enable GET_VOLUME_STATS node capability.
	volumePath := req.GetVolumePath()
	if len(volumePath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "req.VolumePath must be provided")
	}

	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "req.VolumeId must be provided")
	}
	// read and validate config
	err := s.refreshConfig("")
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "Cannot use config file: %s", err)
	}

	volInfo, err := ParseVolumeID(volumeID)
	if err != nil {
		l.Errorf("Got wrong volumeId, VolumeInfo error: %s", err)
		return nil, status.Error(codes.NotFound, fmt.Sprintf("VolumeId is in wrong format: %s", volumeID))
	}

	configName := volInfo.ConfigName
	volumePath = volInfo.Path

	bsrProvider, err, _ := s.resolveBSR(configName, volumePath)
	if err != nil {
		return nil, err
	}

	l.Infof("resolved BrickStor: %s, %+v", bsrProvider.Addr(), volumePath)

	// get BrickStor filesystem information
	available, err := bsrProvider.GetDsAvailCap(volumePath)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "Cannot find filesystem '%s': %s", volumeID, err)
	}

	return &csi.NodeGetVolumeStatsResponse{
		Usage: []*csi.VolumeUsage{
			{
				Unit:      csi.VolumeUsage_BYTES,
				Available: available,
				//TODO add used, total
			},
		},
	}, nil
}

// NodeStageVolume - stage volume
// TODO use this to mount NFS, then do bind mount?
func (s *NodeServer) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (
	*csi.NodeStageVolumeResponse,
	error,
) {
	s.log.WithField("func", "NodeStageVolume()").Warnf("request: '%+v' - not implemented", req)
	return nil, status.Error(codes.Unimplemented, "")
}

// NodeUnstageVolume - unstage volume
// TODO use this to umount NFS?
func (s *NodeServer) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (
	*csi.NodeUnstageVolumeResponse,
	error,
) {
	s.log.WithField("func", "NodeUnstageVolume()").Warnf("request: '%+v' - not implemented", req)
	return nil, status.Error(codes.Unimplemented, "")
}

// NodeExpandVolume - not supported
func (s *NodeServer) NodeExpandVolume(ctx context.Context, req *csi.NodeExpandVolumeRequest) (
	*csi.NodeExpandVolumeResponse,
	error,
) {
	s.log.WithField("func", "NodeExpandVolume()").Warnf("request: '%+v' - not implemented", req)
	return nil, status.Error(codes.Unimplemented, "")
}

// NewNodeServer - create an instance of node service
func NewNodeServer(driver *Driver) (*NodeServer, error) {
	l := driver.log.WithField("cmp", "NodeServer")
	l.Info("create new NodeServer...")

	resolverMap, err := newResolverMap(driver.config, l)
	if err != nil {
		return nil, err
	}

	return &NodeServer{
		nodeID:         driver.nodeID,
		bsrResolverMap: resolverMap,
		config:         driver.config,
		log:            l,
	}, nil
}

// appendMissing returns array with appended string if RegExp doesn't match any
// existing entry.
func appendMissing(strs []string, re *regexp.Regexp, val string) []string {
	if !containsRE(strs, re) {
		return append(strs, val)
	}
	return strs
}

// containsString returns true if array contains a string, otherwise false
func containsString(strs []string, val string) bool {
	for _, s := range strs {
		if s == val {
			return true
		}
	}
	return false
}

// containsRE returns true if any array element matches RegExp.
func containsRE(strs []string, re *regexp.Regexp) bool {
	for _, s := range strs {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}
