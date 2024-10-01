package driver

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/container-storage-interface/spec/lib/go/csi"
	timestamp "github.com/golang/protobuf/ptypes/timestamp"
	"github.com/kubernetes-csi/csi-lib-utils/protosanitizer"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/racktopsystems/brickstor-csi-driver/pkg/bsr"
	"github.com/racktopsystems/brickstor-csi-driver/pkg/config"
)

const TopologyKeyZone = "topology.kubernetes.io/zone"

// supportedControllerCapabilities - driver controller capabilities
var supportedControllerCapabilities = []csi.ControllerServiceCapability_RPC_Type{
	csi.ControllerServiceCapability_RPC_LIST_VOLUMES,
	csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
	csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT,
	csi.ControllerServiceCapability_RPC_LIST_SNAPSHOTS,
	csi.ControllerServiceCapability_RPC_CLONE_VOLUME,
	csi.ControllerServiceCapability_RPC_GET_CAPACITY,
	csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
}

// supportedVolumeCapabilities - driver volume capabilities
var supportedVolumeCapabilities = []*csi.VolumeCapability{
	{
		AccessMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY},
	},
	{
		AccessMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
	},
	{
		AccessMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY},
	},
	{
		AccessMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_SINGLE_WRITER},
	},
	{
		AccessMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER},
	},
}

// ControllerServer - k8s csi driver controller server
type ControllerServer struct {
	bsrResolverMap map[string]*bsr.Resolver
	mutex          sync.Mutex // protects bsrResolverMap
	config         *config.Config
	log            *logrus.Entry

	csi.UnimplementedControllerServer
}

type resolveBsrParams struct {
	datasetPath string // requested dataset name (will be empty for create)
	zone        string // accessibility zone from driver yaml config
	configName  string // this is the node name from the driver yaml config
}

type resolveBsrResponse struct {
	datasetPath string
	bsrProvider bsr.ProviderInterface
	configName  string
}

func (s *ControllerServer) refreshConfig(secret string) error {
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

func (s *ControllerServer) ControllerGetVolume(ctx context.Context,
	req *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (s *ControllerServer) ControllerModifyVolume(ctx context.Context,
	req *csi.ControllerModifyVolumeRequest) (*csi.ControllerModifyVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (s *ControllerServer) resolveBSR(params resolveBsrParams) (resolveBsrResponse, error) {

	l := s.log.WithField("func", "resolveBSR()")
	l.Infof("configName: %v, datasetPath: %v, zone %v",
		params.configName, params.datasetPath, params.zone)

	var resp resolveBsrResponse
	var err error
	if len(params.zone) == 0 {
		resp, err = s.resolveBsrNoZone(params)
	} else {
		resp, err = s.resolveBsrWithZone(params)
	}
	if err != nil {
		code := codes.Internal
		if bsr.ErrIsNotExist(err) {
			code = codes.NotFound
		}
		return resp, status.Errorf(code,
			"Cannot resolve '%s' on any BrickStor: %s", params.datasetPath, err)
	}

	l.Infof("Resolved BrickStor: [%s - %s], %s",
		resp.configName, resp.bsrProvider.Addr(), resp.datasetPath)
	return resp, nil
}

func (s *ControllerServer) resolveBsrNoZone(params resolveBsrParams) (resolveBsrResponse, error) {

	// No zone, pick BrickStor for given "dataset" and "configName".
	l := s.log.WithField("func", "resolveBsrNoZone()")
	l.Infof("Resolving without zone, params: %+v", params)

	datasetPath := params.datasetPath

	if len(params.configName) > 0 {
		if datasetPath == "" {
			cfg, ok := s.config.LookupNode(params.configName)
			if !ok {
				l.Warnf("Missing node %s", params.configName)
				return resolveBsrResponse{},
					status.Error(codes.NotFound, fmt.Sprintf("Node %s was removed", params.configName))
			}

			datasetPath = cfg.DefaultDataset
		}

		s.mutex.Lock()
		resolver := s.bsrResolverMap[params.configName]
		s.mutex.Unlock()

		bsrProvider, err := resolver.Resolve(datasetPath)
		if err != nil {
			return resolveBsrResponse{}, err
		}

		resp := resolveBsrResponse{
			datasetPath: datasetPath,
			bsrProvider: bsrProvider,
			configName:  params.configName,
		}
		return resp, nil
	}

	var bsrProvider bsr.ProviderInterface
	var err error

	s.mutex.Lock()
	defer s.mutex.Unlock()
	for name, resolver := range s.bsrResolverMap {
		if params.datasetPath == "" {
			cfg, ok := s.config.LookupNode(name)
			if !ok {
				l.Warnf("Ignoring missing node %s", name)
				return resolveBsrResponse{},
					status.Error(codes.NotFound, fmt.Sprintf("Node %s was removed", name))
			}
			datasetPath = cfg.DefaultDataset
		}
		bsrProvider, err = resolver.Resolve(datasetPath)
		if bsrProvider != nil {
			resp := resolveBsrResponse{
				datasetPath: datasetPath,
				bsrProvider: bsrProvider,
				configName:  name,
			}
			return resp, err
		}
	}

	if strings.Contains(err.Error(), "unknown authority") {
		return resolveBsrResponse{}, status.Errorf(codes.Unauthenticated,
			fmt.Sprintf("TLS certificate check error: %v", err.Error()))
	}

	return resolveBsrResponse{}, status.Errorf(codes.NotFound,
		fmt.Sprintf("No bsrProvider found for params: %+v", params))
}

func (s *ControllerServer) resolveBsrWithZone(params resolveBsrParams) (resolveBsrResponse, error) {

	// Pick BrickStor with corresponding zone.
	l := s.log.WithField("func", "resolveBsrWithZone()")
	l.Infof("Resolving with zone, params: %+v", params)

	datasetPath := params.datasetPath

	if len(params.configName) > 0 {
		cfg, ok := s.config.LookupNode(params.configName)
		if !ok {
			l.Warnf("Ignoring missing node %s", params.configName)
			return resolveBsrResponse{},
				status.Error(codes.NotFound, fmt.Sprintf("Node %s was removed", params.configName))
		}

		if cfg.Zone != params.zone {
			msg := fmt.Sprintf("requested zone [%s] does not match requested BrickStor name [%s]",
				params.zone, params.configName)
			return resolveBsrResponse{}, status.Errorf(codes.FailedPrecondition, msg)
		}

		if datasetPath == "" {
			cfg, ok := s.config.LookupNode(params.configName)
			if !ok {
				l.Warnf("Ignoring missing node %s", params.configName)
				return resolveBsrResponse{},
					status.Error(codes.NotFound, fmt.Sprintf("Node %s was removed", params.configName))
			}
			datasetPath = cfg.DefaultDataset
		}

		s.mutex.Lock()
		resolver := s.bsrResolverMap[params.configName]
		s.mutex.Unlock()

		bsrProvider, err := resolver.Resolve(datasetPath)
		if err != nil {
			return resolveBsrResponse{}, err
		}

		resp := resolveBsrResponse{
			datasetPath: datasetPath,
			bsrProvider: bsrProvider,
			configName:  params.configName,
		}
		return resp, nil
	}

	var bsrProvider bsr.ProviderInterface
	var err error

	s.mutex.Lock()
	defer s.mutex.Unlock()
	for name, resolver := range s.bsrResolverMap {
		cfg, ok := s.config.LookupNode(name)
		if !ok {
			l.Warnf("Ignoring missing node %s", name)
			return resolveBsrResponse{},
				status.Error(codes.NotFound, fmt.Sprintf("Node %s was removed", name))
		}

		if params.datasetPath == "" {
			datasetPath = cfg.DefaultDataset
		} else {
			datasetPath = params.datasetPath
		}

		if params.zone == cfg.Zone {
			bsrProvider, err = resolver.Resolve(datasetPath)
			if bsrProvider != nil {
				l.Infof("Found dataset %s on BrickStor [%s]", datasetPath, name)
				resp := resolveBsrResponse{
					datasetPath: datasetPath,
					bsrProvider: bsrProvider,
					configName:  name,
				}

				l.Infof("configName: %+v", name)
				return resp, nil
			}
		}
	}

	if strings.Contains(err.Error(), "unknown authority") {
		return resolveBsrResponse{}, status.Errorf(codes.Unauthenticated,
			fmt.Sprintf("TLS certificate check error: %v", err.Error()))
	}
	return resolveBsrResponse{}, status.Errorf(codes.NotFound,
		fmt.Sprintf("No bsrProvider found for params: %+v", params))
}

// ListVolumes SHALL return all available volumes the controller knows about.
// This endpoint supports paging through the list by passing an opaque
// startToken string and limit on each call. As per the CSI specification,
// when paging through the list with multiple calls to this endpoint, it is
// valid to return duplicate volumes or to omit volumes.
//
// For the BrickStor driver, we use the startToken string to hold the current
// node name and offset on that node (e.g. "brickstore7:100").
//
// Because each node from the driver config can be either a standalone server
// or a cluster, we iterate through the address list for the node since datasets
// can reside on any node in the cluster and can even move between calls to
// this function. If a pool moves between calls, the CO might see the datasets
// more than once or might miss them, but that is specifically allowed by the
// CSI spec.
//
// We simplify the code by doing paging on a per-node basis. That is, the
// dataset list is returned for only one node at a time with the nextToken set
// as either a continuation for the current node, or as the beginning of the
// next configured node.
func (s *ControllerServer) ListVolumes(ctx context.Context,
	req *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {

	l := s.log.WithField("func", "ListVolumes()")
	l.Infof("request: '%+v'", protosanitizer.StripSecrets(req))

	startingToken := req.GetStartingToken()
	maxEntries := int(req.GetMaxEntries())
	if maxEntries < 0 {
		return nil, status.Errorf(codes.InvalidArgument, "req.MaxEntries must be 0 or greater, got: %d", maxEntries)
	}

	if maxEntries == 0 {
		// unlimited; for simplicity, use a large number to allow the code to
		// handle an essentially unlimited number of datasets
		maxEntries = 2000000000
	}

	err := s.refreshConfig("")
	if err != nil {
		return nil, status.Errorf(codes.Aborted, "Cannot use config file: %s", err)
	}

	nodes := s.config.NodeNames() // get sorted list of names from config
	offset, nodeIndex, err := s.getPagingValues(startingToken, nodes)
	if err != nil {
		return nil, err
	}

	startingOffset := offset
	var nextToken string
	entries := []*csi.ListVolumesResponse_Entry{}

	for ; nodeIndex < len(nodes); nodeIndex++ {

		configName := nodes[nodeIndex]
		s.mutex.Lock()
		resolver := s.bsrResolverMap[configName]
		s.mutex.Unlock()

		// handle standalone (one address) and cluster nodes (list of addresses)

		configEntry, ok := s.config.LookupNode(configName)
		if !ok {
			// ignore; theoretically the node might have been deleted if config
			// file was changed just after we loaded the config name list
			l.Warnf("Ignoring missing node %s", configName)
			continue
		}
		addresses := strings.Split(configEntry.Address, ",")

		var datasets []bsr.Dataset

		// configName might be a cluster name. To handle a cluster we first
		// get all of the datasets for the nodes in the cluster, then handle the
		// paging within that set. For a standalone node, there will only be
		// one address.

		for _, addr := range addresses {
			bsrProvider, err := resolver.ResolveByAddress(addr)
			if err != nil {
				continue // ignore this node
			}

			nodeDatasets, err := bsrProvider.GetDatasets()
			if err != nil {
				// ignore, might be down
				l.Warnf("Cannot get datasets for %s (%s): %s", configName, addr, err)
			}

			if len(nodeDatasets) == 0 {
				// this can happen if no pools imported on cluster node
				continue
			}

			datasets = append(datasets, nodeDatasets...)
		}

		if len(datasets) == 0 {
			continue // highly unlikely
		}

		for i, ds := range datasets {
			if i < offset {
				continue
			}

			entries = append(entries, &csi.ListVolumesResponse_Entry{
				Volume: &csi.Volume{
					VolumeId: fmt.Sprintf("%s:%s", configName, ds.Path),
				}})

			// if the limit is reached response will contain next token
			if len(entries) == maxEntries {
				break
			}
		}

		if startingOffset+len(entries) < len(datasets) {
			l.Infof("limit (%d) reached while getting datasets for '%s'",
				maxEntries, configName)

			next := offset + len(entries)
			nextToken = fmt.Sprintf("%s:%d", configName, next)

		} else if nodeIndex+1 < len(nodes) {
			// next page for next node
			nextToken = fmt.Sprintf("%s:0", nodes[nodeIndex+1])
		}

		break
	}

	l.Infof("found %d dataset entries(s)", len(entries))

	resp := csi.ListVolumesResponse{
		Entries:   entries,
		NextToken: nextToken,
	}
	return &resp, nil
}

// CreateVolume - creates dataset on BrickStor
func (s *ControllerServer) CreateVolume(ctx context.Context,
	req *csi.CreateVolumeRequest) (res *csi.CreateVolumeResponse, err error) {

	l := s.log.WithField("func", "CreateVolume()")
	l.Infof("request: '%+v'", protosanitizer.StripSecrets(req))
	volumeName := req.GetName()
	if len(volumeName) == 0 {
		return nil, status.Error(codes.InvalidArgument, "req.Name must be provided")
	}
	var secret string
	secrets := req.GetSecrets()
	for _, v := range secrets {
		secret = v
	}

	err = s.refreshConfig(secret)
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "Cannot use config file: %s", err)
	}

	volumeCapabilities := req.GetVolumeCapabilities()
	if volumeCapabilities == nil {
		return nil, status.Error(codes.InvalidArgument, "req.VolumeCapabilities must be provided")
	}

	for _, reqC := range volumeCapabilities {
		if !validateVolumeCapability(reqC) {
			message := fmt.Sprintf("Driver does not support volume capability mode: %s", reqC.GetAccessMode().GetMode())
			l.Warn(message)
			return nil, status.Error(codes.FailedPrecondition, message)
		}
	}

	var sourceSnapshotId string
	var sourceVolumeId string
	var volumePath string
	var contentSource *csi.VolumeContentSource
	var bsrProvider bsr.ProviderInterface
	var resolveResp resolveBsrResponse

	if volumeContentSource := req.GetVolumeContentSource(); volumeContentSource != nil {
		if sourceSnapshot := volumeContentSource.GetSnapshot(); sourceSnapshot != nil {
			sourceSnapshotId = sourceSnapshot.GetSnapshotId()
			contentSource = req.GetVolumeContentSource()
		} else if sourceVolume := volumeContentSource.GetVolume(); sourceVolume != nil {
			sourceVolumeId = sourceVolume.GetVolumeId()
			contentSource = req.GetVolumeContentSource()
		} else {
			return nil, status.Errorf(
				codes.InvalidArgument,
				"Only snapshots and volumes are supported as volume content source, but got type: %s",
				volumeContentSource.GetType(),
			)
		}
	}

	// plugin specific parameters passed in as opaque key-value pairs
	reqParams := req.GetParameters()
	if reqParams == nil {
		l.Infof("request params: none")
		reqParams = make(map[string]string)
	} else {
		l.Infof("request params: '%+v'", reqParams)
	}

	// get dataset path and node from runtime params; will use first node and
	// its default path if neither specified
	var datasetPath string
	if v, ok := reqParams["dataset"]; ok {
		datasetPath = v
	}
	var configName string
	if v, ok := reqParams["configName"]; ok {
		configName = v
	}

	accessReqs := req.GetAccessibilityRequirements()
	zone := s.pickAvailabilityZone(accessReqs)
	params := resolveBsrParams{
		datasetPath: datasetPath,
		zone:        zone,
		configName:  configName,
	}

	// get requested volume size from runtime params, set default if not specified
	capacityBytes := req.GetCapacityRange().GetRequiredBytes()
	if sourceSnapshotId != "" {
		// create new volume using existing snapshot
		var volInfo VolumeInfo
		volInfo, err = ParseVolumeID(sourceSnapshotId)
		if err != nil {
			l.Errorf("Got wrong volumeId, VolumeInfo error: %s", err)
			return nil, status.Error(codes.NotFound, fmt.Sprintf("SnapshotId is in wrong format: %s", sourceSnapshotId))
		}

		params.configName = volInfo.ConfigName
		resolveResp, err = s.resolveBSR(params)
		if err != nil {
			return nil, err
		}

		bsrProvider = resolveResp.bsrProvider
		datasetPath = resolveResp.datasetPath
		volumePath = filepath.Join(datasetPath, volumeName)
		err = s.createNewVolFromSnap(bsrProvider, volInfo.Path, volumePath, capacityBytes)

	} else if sourceVolumeId != "" {
		// clone existing volume
		var volInfo VolumeInfo
		volInfo, err = ParseVolumeID(sourceVolumeId)
		if err != nil {
			l.Errorf("Got wrong volumeId, VolumeInfo error: %s", err)
			return nil, status.Error(codes.NotFound, fmt.Sprintf("VolumeId is in wrong format: %s", sourceVolumeId))
		}

		params.configName = volInfo.ConfigName
		resolveResp, err = s.resolveBSR(params)
		if err != nil {
			return nil, err
		}

		bsrProvider = resolveResp.bsrProvider
		datasetPath = resolveResp.datasetPath
		volumePath = filepath.Join(datasetPath, volumeName)
		err = s.createClonedVol(bsrProvider, volInfo.Path, volumePath, volumeName, capacityBytes)

	} else {
		if datasetPath == "" && configName == "" {
			// pick first configured node, resolver will use its default dataset
			nodes := s.config.NodeNames()
			if len(nodes) == 0 {
				return nil, status.Error(codes.NotFound, noBsrNodes)
			}
			params.configName = nodes[0]
		}

		resolveResp, err = s.resolveBSR(params)
		if err != nil {
			return nil, err
		}

		bsrProvider = resolveResp.bsrProvider
		datasetPath = resolveResp.datasetPath
		volumePath = filepath.Join(datasetPath, volumeName)
		err = s.createNewVol(bsrProvider, volumePath, capacityBytes)
	}
	if err != nil {
		l.Errorf("%s", err)
		return nil, err
	}

	cfg, ok := s.config.LookupNode(resolveResp.configName)
	if !ok {
		l.Warnf("Ignoring missing node %s", resolveResp.configName)
		return nil, status.Error(codes.NotFound,
			fmt.Sprintf("Node %s was removed", resolveResp.configName))
	}

	mountPointPermissions := ""
	if v, ok := reqParams["mountPointPermissions"]; ok {
		mountPointPermissions = v
	} else {
		mountPointPermissions = cfg.MountPointPermissions
	}

	res = &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			ContentSource: contentSource,
			VolumeId:      fmt.Sprintf("%s:%s", resolveResp.configName, volumePath),
			CapacityBytes: capacityBytes,
			VolumeContext: map[string]string{
				"dataIp":                reqParams["dataIp"],
				"mountOptions":          reqParams["mountOptions"],
				"mountFsType":           reqParams["mountFsType"],
				"mountPointPermissions": mountPointPermissions,
			},
		},
	}
	if len(zone) > 0 {
		res.Volume.AccessibleTopology = []*csi.Topology{
			{
				Segments: map[string]string{TopologyKeyZone: zone},
			},
		}
	}

	// Create NFS share if passed in params
	if v, ok := reqParams["nfsAccessList"]; ok {
		if err = s.createNfsShare(volumePath, v, bsrProvider); err != nil {
			return nil, err
		}
	}

	l.Infof("volume '%s' has been created", volumePath)
	return res, nil
}

func (s *ControllerServer) createNfsShare(volumePath string, reqParams string,
	bsrProvider bsr.ProviderInterface) (err error) {

	l := s.log.WithField("func", "createNfsShare()")
	l.Infof("volumePath: %+v, reqParams: %+v, bsrProvider: %s",
		volumePath, reqParams, bsrProvider.Addr())

	return bsrProvider.CreateNfsShare(volumePath, reqParams)
}

func (s *ControllerServer) createNewVol(bsrProvider bsr.ProviderInterface,
	volumePath string, capacityBytes int64) error {

	l := s.log.WithField("func", "createNewVol()")
	l.Infof("bsrProvider: %s, volumePath: %s", bsrProvider.Addr(), volumePath)

	err := bsrProvider.CreateDataset(volumePath, capacityBytes)
	if err != nil {
		if bsr.ErrIsExists(err) {
			// As per the CSI spec, create volume MUST be idempotent (if the
			// existing volume is the correct size)
			ds, err := bsrProvider.GetDataset(volumePath)
			if err != nil {
				return status.Errorf(codes.Internal,
					"Volume '%s' already exists, but size check failed: %s",
					volumePath, err)
			}

			if capacityBytes != 0 && ds.GetRefQuotaSize() != capacityBytes {
				return status.Errorf(codes.AlreadyExists,
					"Existing volume '%s' has incorrect size (req=%d, curr=%d)",
					volumePath, capacityBytes, ds.GetRefQuotaSize())
			}

			l.Infof("volume '%s' already exists and can be used", volumePath)
			return nil
		}

		return status.Errorf(codes.Internal, "Cannot create volume '%s': %s",
			volumePath, err)
	}

	l.Infof("volume '%s' has been created", volumePath)
	return nil
}

// create new volume using existing snapshot
func (s *ControllerServer) createNewVolFromSnap(bsrProvider bsr.ProviderInterface,
	snapName string, volumePath string, capacityBytes int64) error {

	l := s.log.WithField("func", "createNewVolFromSnap()")
	l.Infof("snapshot: %s", snapName)

	snapshot, err := bsrProvider.GetSnapshot(snapName)
	if err != nil {
		message := fmt.Sprintf("Failed to find snapshot '%s': %s", snapName, err)
		if bsr.ErrIsNotExist(err) {
			return status.Error(codes.NotFound, message)
		}
		return status.Error(codes.NotFound, message)
	}

	err = bsrProvider.CloneSnapshot(snapshot.Path, volumePath, capacityBytes)
	if err != nil {
		if bsr.ErrIsExists(err) {
			l.Infof("volume '%s' already exists and can be used", volumePath)
			return nil
		}

		return status.Errorf(codes.Internal,
			"Cannot create volume '%s' using snapshot '%s': %s",
			volumePath, snapshot.Path, err)
	}

	l.Infof("volume '%s' has been created using snapshot '%s'", volumePath, snapshot.Path)
	return nil
}

func (s *ControllerServer) createClonedVol(bsrProvider bsr.ProviderInterface,
	sourceVolumeID string, volumePath string, volumeName string,
	capacityBytes int64) error {

	l := s.log.WithField("func", "createClonedVol()")
	l.Infof("clone volume source: %+v, target: %+v", sourceVolumeID, volumePath)

	snapName := fmt.Sprintf("k8s-clone-snapshot-%s", volumeName)
	snapshotPath := fmt.Sprintf("%s@%s", sourceVolumeID, snapName)

	_, err := s.CreateSnapshotOnBSR(bsrProvider, sourceVolumeID, snapName)
	if err != nil {
		return err
	}

	err = bsrProvider.CloneSnapshot(snapshotPath, volumePath, capacityBytes)
	if err != nil {
		if bsr.ErrIsExists(err) {
			l.Infof("volume '%s' already exists and can be used", volumePath)
			return nil
		}

		return status.Errorf(codes.NotFound,
			"Cannot create volume '%s' using snapshot '%s': %s",
			volumePath, snapshotPath, err)
	}

	l.Infof("successfully created cloned volume %+v", volumePath)
	return nil
}

// DeleteVolume - destroys FS on BrickStor
func (s *ControllerServer) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {

	l := s.log.WithField("func", "DeleteVolume()")
	l.Infof("request: '%+v'", protosanitizer.StripSecrets(req))

	var secret string
	secrets := req.GetSecrets()
	for _, v := range secrets {
		secret = v
	}
	err := s.refreshConfig(secret)
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "Cannot use config file: %s", err)
	}

	volumeId := req.GetVolumeId()
	if len(volumeId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID must be provided")
	}

	volInfo, err := ParseVolumeID(volumeId)
	if err != nil {
		l.Infof("Got wrong volumeId, but that is OK for deletion")
		l.Infof("VolumeInfo error: %s", err)
		return &csi.DeleteVolumeResponse{}, nil
	}

	params := resolveBsrParams{
		datasetPath: volInfo.Path,
		configName:  volInfo.ConfigName,
	}

	resolveResp, err := s.resolveBSR(params)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			l.Infof("volume '%s' not found, that's OK for deletion request", volInfo.Path)
			return &csi.DeleteVolumeResponse{}, nil
		}
		return nil, err
	}
	bsrProvider := resolveResp.bsrProvider

	// if here, than volumePath exists
	err = bsrProvider.DestroyDataset(volInfo.Path)
	if err != nil && !bsr.ErrIsNotExist(err) {
		return nil, status.Errorf(
			codes.Internal,
			"Cannot delete '%s' volume: %s",
			volInfo.Path,
			err,
		)
	}

	l.Infof("volume '%s' has been deleted", volInfo.Path)
	return &csi.DeleteVolumeResponse{}, nil
}

func (s *ControllerServer) CreateSnapshotOnBSR(bsrProvider bsr.ProviderInterface,
	volumePath, snapName string) (bsr.Snapshot, error) {

	l := s.log.WithField("func", "CreateSnapshotOnBSR()")

	snapshotPath := fmt.Sprintf("%s@%s", volumePath, snapName)

	// CreateSnapshot must be idempotent. The error we get back from trying to
	// create a snap that already exists is "snapshot was not created", which
	// doesn't provide enough information to know why. Thus, to make this
	// idempotent (as best we can), check for existence first.
	if snap, err := bsrProvider.GetSnapshot(snapshotPath); err == nil {
		l.Infof("snapshot '%s' already exists", snapshotPath)
		return snap, nil
	}

	l.Infof("creating snapshot '%s'", snapshotPath)
	snapshot, err := bsrProvider.CreateSnapshot(volumePath, snapName)
	if err != nil {
		return bsr.Snapshot{}, status.Errorf(codes.Internal,
			"Cannot create snapshot '%s': %s", snapName, err)
	}

	l.Infof("successfully created snapshot %s@%s", volumePath, snapName)
	return snapshot, nil
}

// CreateSnapshot creates a snapshot of given volume. This call MUST be
// idempotent.
func (s *ControllerServer) CreateSnapshot(ctx context.Context,
	req *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {

	l := s.log.WithField("func", "CreateSnapshot()")
	l.Infof("request: '%+v'", protosanitizer.StripSecrets(req))

	err := s.refreshConfig("")
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "Cannot use config file: %s", err)
	}

	sourceVolumeId := req.GetSourceVolumeId()
	if len(sourceVolumeId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Snapshot source volume ID must be provided")
	}

	volInfo, err := ParseVolumeID(sourceVolumeId)
	if err != nil {
		l.Infof("VolumeInfo error: %s", err)
		return nil, err
	}

	name := req.GetName()
	if len(name) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Snapshot name must be provided")
	}

	params := resolveBsrParams{
		datasetPath: volInfo.Path,
		configName:  volInfo.ConfigName,
	}

	resolveResp, err := s.resolveBSR(params)
	if err != nil {
		return nil, err
	}

	createdSnapshot, err := s.CreateSnapshotOnBSR(resolveResp.bsrProvider, volInfo.Path, name)
	if err != nil {
		return nil, err
	}
	creationTime := &timestamp.Timestamp{
		Seconds: createdSnapshot.CreationTime.Unix(),
	}

	snapshotId := fmt.Sprintf("%s:%s@%s", resolveResp.configName, volInfo.Path, name)
	res := &csi.CreateSnapshotResponse{
		Snapshot: &csi.Snapshot{
			SnapshotId:     snapshotId,
			SourceVolumeId: sourceVolumeId,
			CreationTime:   creationTime,
			ReadyToUse:     true,
			// SizeByte: 0 // size of zero means it is unspecified
		},
	}

	if bsr.ErrIsExists(err) {
		l.Infof("snapshot '%s' already exists and can be used", snapshotId)
		return res, nil
	}

	l.Infof("snapshot '%s' has been created", snapshotId)
	return res, nil
}

// DeleteSnapshot deletes snapshots
func (s *ControllerServer) DeleteSnapshot(ctx context.Context, req *csi.DeleteSnapshotRequest) (
	*csi.DeleteSnapshotResponse,
	error,
) {
	l := s.log.WithField("func", "DeleteSnapshot()")
	l.Infof("request: '%+v'", protosanitizer.StripSecrets(req))

	err := s.refreshConfig("")
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "Cannot use config file: %s", err)
	}

	snapshotId := req.GetSnapshotId()
	if len(snapshotId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Snapshot ID must be provided")
	}

	volume := ""
	snapshot := ""
	splittedString := strings.Split(snapshotId, "@")
	if len(splittedString) == 2 {
		volume = splittedString[0]
		snapshot = splittedString[1]
	} else {
		l.Infof("snapshot '%s' not found, that's OK for deletion request", snapshotId)
		return &csi.DeleteSnapshotResponse{}, nil
	}

	volInfo, err := ParseVolumeID(volume)
	if err != nil {
		l.Infof("VolumeInfo error: %s", err)
		return nil, err
	}

	params := resolveBsrParams{
		datasetPath: volInfo.Path,
		configName:  volInfo.ConfigName,
	}

	resolveResp, err := s.resolveBSR(params)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			l.Infof("snapshot '%s' not found, that's OK for deletion request", snapshotId)
			return &csi.DeleteSnapshotResponse{}, nil
		}
		return nil, err
	}
	bsrProvider := resolveResp.bsrProvider

	// if here, than volumePath exists
	snapshotPath := strings.Join([]string{volInfo.Path, snapshot}, "@")
	err = bsrProvider.DestroySnapshot(snapshotPath)
	if err != nil && !bsr.ErrIsNotExist(err) {
		message := fmt.Sprintf("Failed to delete snapshot '%s'", snapshotPath)
		return nil, status.Errorf(codes.Internal, "%s: %s", message, err)
	}

	l.Infof("snapshot '%s' has been deleted", snapshotPath)
	return &csi.DeleteSnapshotResponse{}, nil
}

// ListSnapshots SHALL return all snapshots within the given parameters,
// regardless of how they were created. As per the CSI specification, when
// paging through the list with multiple calls to this endpoint, it is
// valid to return duplicate snaps or to omit snaps.
//
// This endpoint implements the same startToken layout as documented for
// the ListVolumes endpoint. Because there can be so many snaps on a BrickStor,
// we simplify the code by doing paging on a per-node basis. That is, the
// snap list is returned for only one node at a time with the nextToken set as
// either a continuation on the current node, or as the beginning of the next
// configured node.
func (s *ControllerServer) ListSnapshots(ctx context.Context,
	req *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {

	l := s.log.WithField("func", "ListSnapshots()")
	l.Infof("request: '%+v'", protosanitizer.StripSecrets(req))

	err := s.refreshConfig("")
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "Cannot use config file: %s", err)
	}

	// request is for one named snapshot
	if req.GetSnapshotId() != "" {
		return s.getSingleSnap(req.GetSnapshotId())
	}

	// request is for all snapshots for one name volume
	if req.GetSourceVolumeId() != "" {
		return s.getDsSnapList(req)
	}

	// request is for all snaps (with paging)
	startingToken := req.GetStartingToken()
	maxEntries := int(req.GetMaxEntries())
	if maxEntries < 0 {
		return nil, status.Errorf(codes.InvalidArgument, "req.MaxEntries must be 0 or greater, got: %d", maxEntries)
	}

	if maxEntries == 0 {
		// unlimited; for simplicity, use a large number to allow the code to
		// handle an essentially unlimited number of snapshots
		maxEntries = 2000000000
	}

	nodes := s.config.NodeNames() // get sorted list of names from config
	offset, nodeIndex, err := s.getPagingValues(startingToken, nodes)
	if err != nil {
		return nil, err
	}

	startingOffset := offset
	var nextToken string
	entries := []*csi.ListSnapshotsResponse_Entry{}

	for ; nodeIndex < len(nodes); nodeIndex++ {

		configName := nodes[nodeIndex]
		s.mutex.Lock()
		resolver := s.bsrResolverMap[configName]
		s.mutex.Unlock()

		// handle standalone (one address) and cluster nodes (list of addresses)

		configEntry, ok := s.config.LookupNode(configName)
		if !ok {
			// ignore; theoretically the node might have been deleted if config
			// file was changed just after we loaded the config name list
			l.Warnf("Ignoring missing node %s", configName)
			continue
		}
		addresses := strings.Split(configEntry.Address, ",")

		var snapshots []bsr.Snapshot

		// configName might be a cluster name. To handle a cluster we first
		// get all of the snaps for the nodes in the cluster, then handle the
		// paging within that set. For a standalone node, there will only be
		// one address.

		for _, addr := range addresses {
			bsrProvider, err := resolver.ResolveByAddress(addr)
			if err != nil {
				continue // ignore this node
			}

			datasets, err := bsrProvider.GetDatasets()
			if err != nil {
				continue // ignore this node (maybe down)
			}

			if len(datasets) == 0 {
				// this can happen if no pools imported on cluster node
				continue
			}

			dsNames := make([]string, len(datasets))
			for i, ds := range datasets {
				dsNames[i] = ds.Path
			}

			// get all snaps for the named datasets on this node
			snaps, err := bsrProvider.GetSnapshots(dsNames)
			if err != nil {
				continue // ignore this node
			}

			snapshots = append(snapshots, snaps...)
		}

		if len(snapshots) == 0 {
			continue // highly unlikely
		}

		for i, snap := range snapshots {
			if i < offset {
				continue
			}

			entries = append(entries,
				convertSnapToCSISnap(snap, configName))

			// if the limit is reached response will contain next token
			if len(entries) == maxEntries {
				break
			}
		}

		if startingOffset+len(entries) < len(snapshots) {
			l.Infof("limit (%d) reached while getting snaps for '%s'",
				maxEntries, configName)

			next := offset + len(entries)
			nextToken = fmt.Sprintf("%s:%d", configName, next)

		} else if nodeIndex+1 < len(nodes) {
			// next page for next node
			nextToken = fmt.Sprintf("%s:0", nodes[nodeIndex+1])
		}

		break
	}

	l.Infof("found %d snapshot entries(s)", len(entries))

	response := csi.ListSnapshotsResponse{
		Entries:   entries,
		NextToken: nextToken,
	}

	return &response, nil
}

func (s *ControllerServer) getSingleSnap(snapshotId string) (*csi.ListSnapshotsResponse, error) {

	l := s.log.WithField("func", "getSingleSnap()")
	l.Infof("Snapshot path: %s", snapshotId)

	response := csi.ListSnapshotsResponse{
		Entries: []*csi.ListSnapshotsResponse_Entry{},
	}

	flds := strings.Split(snapshotId, "@")
	if len(flds) != 2 {
		// bad snapshotID format, driver should return empty response
		l.Infof("Bad snapshot format: %v", flds)
		return &response, nil
	}

	volInfo, err := ParseVolumeID(flds[0])
	if err != nil {
		l.Infof("VolumeInfo error: %s", err)
		return nil, err
	}

	params := resolveBsrParams{
		datasetPath: volInfo.Path,
		configName:  volInfo.ConfigName,
	}

	resolveResp, err := s.resolveBSR(params)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			l.Infof("filesystem '%s' not found, that's OK for list request", volInfo.Path)
			return &response, nil
		}
		return nil, err
	}
	bsrProvider := resolveResp.bsrProvider
	snapshotPath := fmt.Sprintf("%s@%s", volInfo.Path, flds[1])
	snapshot, err := bsrProvider.GetSnapshot(snapshotPath)
	if err != nil {
		if bsr.ErrIsNotExist(err) {
			return &response, nil
		}
		return nil, status.Errorf(codes.Internal,
			"Cannot get snapshot '%s' for snapshot list: %s", snapshotPath, err)
	}

	response.Entries = append(response.Entries, convertSnapToCSISnap(snapshot, resolveResp.configName))
	l.Infof("snapshot '%s' found for '%s' filesystem", snapshot.Path, volInfo.Path)

	return &response, nil
}

// getPagingValues handles the setup from the startToken for the offset and
// nodeIndex when paging through either ListVolumes or ListSnapshots results.
func (s *ControllerServer) getPagingValues(startToken string, nodes []string) (int, int, error) {

	// paranoid check
	if len(nodes) == 0 {
		return 0, 0, status.Errorf(codes.Aborted, noBsrNodes)
	}

	// Start with the sorted list of node name entries from the map.
	// If startTok == ""
	//     begin with the first node's snaps
	// Otherwise, split tok
	//     begin with the next set of entries from the node name and offset

	offset := 0
	nodeIndex := -1
	if startToken != "" {
		tok := strings.Split(startToken, ":")
		if len(tok) != 2 {
			return 0, 0, status.Errorf(codes.Aborted,
				"Invalid starting token; missing node name and offset")
		}

		tokName := tok[0]
		val, err := strconv.ParseInt(tok[1], 10, 32)
		if err != nil {
			return 0, 0, status.Errorf(codes.Aborted,
				"Invalid starting token; bad offset")
		}

		offset = int(val)

		for i := 0; i < len(nodes); i++ {
			if nodes[i] == tokName {
				nodeIndex = i
				break
			}
		}

		if nodeIndex == -1 {
			// node name not found
			return 0, 0, status.Errorf(codes.Aborted,
				"Invalid starting token; unknown node %s", tokName)
		}
	}

	if nodeIndex == -1 {
		nodeIndex = 0
	}

	return offset, nodeIndex, nil
}

// getDsSnapList uses a different starting token format from the full
// ListVolumes and ListSnapshots style. In the limited case here, the
// starting token is the name of the last snapshot returned.
func (s *ControllerServer) getDsSnapList(req *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {

	l := s.log.WithField("func", "getDsSnapList()")

	volumeId := req.GetSourceVolumeId()
	flds := strings.Split(volumeId, ":")
	var volumePath string
	params := resolveBsrParams{}
	if len(flds) == 2 {
		params.configName = flds[0]
		volumePath = flds[1]
	} else {
		volumePath = volumeId
	}
	params.datasetPath = volumePath
	resolveResp, err := s.resolveBSR(params)
	l.Infof("node: %s", resolveResp.configName)

	if err != nil {
		l.Infof("volume '%s' not found (OK for list snap request)", volumePath)
		response := csi.ListSnapshotsResponse{
			Entries: []*csi.ListSnapshotsResponse_Entry{},
		}
		return &response, nil
	}

	bsrProvider := resolveResp.bsrProvider
	configName := resolveResp.configName
	startingToken := req.GetStartingToken()
	maxEntries := int(req.GetMaxEntries())

	response := csi.ListSnapshotsResponse{
		Entries: []*csi.ListSnapshotsResponse_Entry{},
	}

	datasets := []string{volumePath}
	snapshots, err := bsrProvider.GetSnapshots(datasets)
	if err != nil {
		return nil, status.Errorf(codes.Internal,
			"Cannot get snapshot list for '%s': %s", configName, err)
	}

	for i, snapshot := range snapshots {
		// skip all snaps before starting token
		if snapshot.Path == startingToken {
			startingToken = ""
		}
		if startingToken != "" {
			continue
		}

		response.Entries = append(response.Entries,
			convertSnapToCSISnap(snapshot, configName))

		// if the limit is reached (and specified) than set next token
		// to be the snap name
		if maxEntries != 0 && len(response.Entries) == maxEntries {
			l.Infof("limit (%d) reached while getting snaps for '%s'",
				maxEntries, configName)

			if i+1 < len(snapshots) {
				// next snapshot exists
				response.NextToken = snapshots[i+1].Path
			}

			break
		}
	}

	l.Infof("found %d snapshot(s) for node '%s'", len(response.Entries), configName)

	return &response, nil
}

func convertSnapToCSISnap(snap bsr.Snapshot, configName string) *csi.ListSnapshotsResponse_Entry {
	return &csi.ListSnapshotsResponse_Entry{
		Snapshot: &csi.Snapshot{
			SnapshotId:     fmt.Sprintf("%s:%s@%s", configName, snap.Path, snap.Name),
			SourceVolumeId: fmt.Sprintf("%s:%s", configName, snap.Path),
			CreationTime: &timestamp.Timestamp{
				Seconds: snap.CreationTime.Unix(),
			},
			ReadyToUse: true,
			// SizeByte: 0 // size of zero means it is unspecified
		},
	}
}

// ValidateVolumeCapabilities validates volume capabilities
// Shall return confirmed only if all the volume
// capabilities specified in the request are supported.
func (s *ControllerServer) ValidateVolumeCapabilities(ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (
	*csi.ValidateVolumeCapabilitiesResponse,
	error,
) {
	l := s.log.WithField("func", "ValidateVolumeCapabilities()")
	l.Infof("request: '%+v'", protosanitizer.StripSecrets(req))

	volumeId := req.GetVolumeId()
	if len(volumeId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID must be provided")
	}

	// not clear why we need to check VolumeID format only, without checking pathes
	volInfo, err := ParseVolumeID(volumeId)
	if err != nil {
		l.Errorf("Got wrong volumeId, VolumeInfo error: %s", err)
		return nil, status.Error(codes.NotFound, fmt.Sprintf("VolumeId is in wrong format: %s", volumeId))
	}

	volumeCapabilities := req.GetVolumeCapabilities()
	if volumeCapabilities == nil {
		return nil, status.Error(codes.InvalidArgument, "req.VolumeCapabilities must be provided")
	}

	// volume attributes are passed from ControllerServer.CreateVolume()
	volumeContext := req.GetVolumeContext()

	var secret string
	secrets := req.GetSecrets()
	for _, v := range secrets {
		secret = v
	}
	err = s.refreshConfig(secret)
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "Cannot use config file: %s", err)
	}

	params := resolveBsrParams{
		datasetPath: volInfo.Path,
		configName:  volInfo.ConfigName,
	}

	bsrProvider, err := s.resolveBSR(params)
	if err != nil {
		return nil, err
	}

	l.Infof("resolved BrickStor: %s, %+v", bsrProvider, params)

	for _, reqC := range volumeCapabilities {
		supported := validateVolumeCapability(reqC)
		l.Infof("requested capability: '%s', supported: %t", reqC.GetAccessMode().GetMode(), supported)
		if !supported {
			message := fmt.Sprintf("Driver does not support volume capability mode: %s", reqC.GetAccessMode().GetMode())
			l.Warn(message)
			return &csi.ValidateVolumeCapabilitiesResponse{
				Message: message,
			}, nil
		}
	}

	return &csi.ValidateVolumeCapabilitiesResponse{
		Confirmed: &csi.ValidateVolumeCapabilitiesResponse_Confirmed{
			VolumeCapabilities: supportedVolumeCapabilities,
			VolumeContext:      volumeContext,
		},
	}, nil
}

// ControllerGetCapabilities - controller capabilities
func (s *ControllerServer) ControllerGetCapabilities(ctx context.Context, req *csi.ControllerGetCapabilitiesRequest) (
	*csi.ControllerGetCapabilitiesResponse,
	error,
) {
	s.log.WithField("func", "ControllerGetCapabilities()").Infof("request: '%+v'", req)

	var capabilities []*csi.ControllerServiceCapability
	for _, c := range supportedControllerCapabilities {
		capabilities = append(capabilities, newControllerServiceCapability(c))
	}
	return &csi.ControllerGetCapabilitiesResponse{
		Capabilities: capabilities,
	}, nil
}

func newControllerServiceCapability(cap csi.ControllerServiceCapability_RPC_Type) *csi.ControllerServiceCapability {
	return &csi.ControllerServiceCapability{
		Type: &csi.ControllerServiceCapability_Rpc{
			Rpc: &csi.ControllerServiceCapability_RPC{
				Type: cap,
			},
		},
	}
}

func validateVolumeCapability(reqVolumeCapability *csi.VolumeCapability) bool {
	// block is not supported
	if reqVolumeCapability.GetBlock() != nil {
		return false
	}

	reqMode := reqVolumeCapability.GetAccessMode().GetMode()
	for _, volumeCapability := range supportedVolumeCapabilities {
		if volumeCapability.GetAccessMode().GetMode() == reqMode {
			return true
		}
	}

	return false
}

func (s *ControllerServer) GetCapacity(ctx context.Context, req *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {

	l := s.log.WithField("func", "GetCapacity()")
	l.Infof("request: '%+v'", protosanitizer.StripSecrets(req))

	reqParams := req.GetParameters()
	if reqParams == nil {
		reqParams = make(map[string]string)
	}

	// get dataset path from runtime params, set default if not specified
	var datasetPath string
	if v, ok := reqParams["dataset"]; ok {
		datasetPath = v
	}

	params := resolveBsrParams{
		datasetPath: datasetPath,
	}
	resolveResp, err := s.resolveBSR(params)
	if err != nil {
		return nil, err
	}

	bsrProvider := resolveResp.bsrProvider
	ds, err := bsrProvider.GetDataset(resolveResp.datasetPath)
	if err != nil {
		return nil, err
	}

	availableCapacity := ds.GetRefQuotaSize()
	l.Infof("Available capacity: '%+v' bytes", availableCapacity)
	return &csi.GetCapacityResponse{
		AvailableCapacity: availableCapacity,
	}, nil
}

// ControllerPublishVolume - not supported
func (s *ControllerServer) ControllerPublishVolume(ctx context.Context, req *csi.ControllerPublishVolumeRequest) (
	*csi.ControllerPublishVolumeResponse,
	error,
) {
	s.log.WithField("func", "ControllerPublishVolume()").Warnf("request: '%+v' - not implemented", req)
	return nil, status.Error(codes.Unimplemented, "")
}

// ControllerUnpublishVolume - not supported
func (s *ControllerServer) ControllerUnpublishVolume(ctx context.Context, req *csi.ControllerUnpublishVolumeRequest) (
	*csi.ControllerUnpublishVolumeResponse,
	error,
) {
	s.log.WithField("func", "ControllerUnpublishVolume()").Warnf("request: '%+v' - not implemented", req)
	return nil, status.Error(codes.Unimplemented, "")
}

func (s *ControllerServer) ControllerExpandVolume(ctx context.Context, req *csi.ControllerExpandVolumeRequest) (
	*csi.ControllerExpandVolumeResponse,
	error,
) {
	l := s.log.WithField("func", "ControllerExpandVolume()")
	l.Infof("request: '%+v'", protosanitizer.StripSecrets(req))

	var secret string
	secrets := req.GetSecrets()
	for _, v := range secrets {
		secret = v
	}
	err := s.refreshConfig(secret)
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "Cannot use config file: %s", err)
	}
	capacityBytes := req.GetCapacityRange().GetRequiredBytes()
	if capacityBytes == 0 {
		return nil, status.Error(codes.InvalidArgument, "GetRequiredBytes must be >0")
	}

	volumeId := req.GetVolumeId()
	if len(volumeId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID must be provided")
	}

	volInfo, err := ParseVolumeID(volumeId)
	if err != nil {
		l.Infof("VolumeInfo error: %s", err)
		return nil, err
	}

	params := resolveBsrParams{
		datasetPath: volInfo.Path,
		configName:  volInfo.ConfigName,
	}

	resolveResp, err := s.resolveBSR(params)
	if err != nil {
		return nil, err
	}
	bsrProvider := resolveResp.bsrProvider

	l.Infof("expanding volume %+v to %+v bytes", volInfo.Path, capacityBytes)
	err = bsrProvider.UpdateDataset(volInfo.Path, capacityBytes)
	if err != nil {
		return nil, fmt.Errorf("Failed to expand volume volume %s: %s", volInfo.Path, err)
	}
	return &csi.ControllerExpandVolumeResponse{
		CapacityBytes: capacityBytes,
	}, nil
}

func (s *ControllerServer) pickAvailabilityZone(requirement *csi.TopologyRequirement) string {
	l := s.log.WithField("func", "s.pickAvailabilityZone()")
	l.Infof("AccessibilityRequirements: '%+v'", requirement)
	if requirement == nil {
		return ""
	}
	for _, topology := range requirement.GetPreferred() {
		zone, exists := topology.GetSegments()[TopologyKeyZone]
		if exists {
			return zone
		}
	}
	for _, topology := range requirement.GetRequisite() {
		zone, exists := topology.GetSegments()[TopologyKeyZone]
		if exists {
			return zone
		}
	}
	return ""
}

// NewControllerServer - create an instance of controller service
func NewControllerServer(driver *Driver) (*ControllerServer, error) {
	l := driver.log.WithField("cmp", "ControllerServer")
	l.Info("create new ControllerServer...")

	resolverMap, err := newResolverMap(driver.config, l)
	if err != nil {
		return nil, err
	}

	l.Infof("Resolver map: %+v", resolverMap)
	return &ControllerServer{
		bsrResolverMap: resolverMap,
		config:         driver.config,
		log:            l,
	}, nil
}
