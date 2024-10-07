package driver

import (
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/racktopsystems/brickstor-csi-driver/pkg/config"
)

// IdentityServer - k8s csi driver identity server
type IdentityServer struct {
	csi.UnimplementedIdentityServer
	config *config.Config
	log    *logrus.Entry
}

// GetPluginInfo - return plugin info
func (ids *IdentityServer) GetPluginInfo(ctx context.Context,
	req *csi.GetPluginInfoRequest) (*csi.GetPluginInfoResponse, error) {

	l := ids.log.WithField("func", "GetPluginInfo()")
	l.Infof("request: '%+v'", req)

	res := csi.GetPluginInfoResponse{
		Name:          Name,
		VendorVersion: Version,
	}

	l.Debugf("response: '%s (%s)' %+v", res.Name, res.VendorVersion, res.Manifest)

	return &res, nil
}

// GetPluginCapabilities - get plugin capabilities
func (ids *IdentityServer) GetPluginCapabilities(ctx context.Context,
	req *csi.GetPluginCapabilitiesRequest) (*csi.GetPluginCapabilitiesResponse, error) {

	ids.log.WithField("func", "GetPluginCapabilities()").Infof("request: '%+v'", req)

	return &csi.GetPluginCapabilitiesResponse{
		Capabilities: []*csi.PluginCapability{
			{
				Type: &csi.PluginCapability_Service_{
					Service: &csi.PluginCapability_Service{
						Type: csi.PluginCapability_Service_CONTROLLER_SERVICE,
					},
				},
			}, {
				// From CSI spec.
				// VOLUME_ACCESSIBILITY_CONSTRAINTS indicates that the volumes
				// for this plugin MAY NOT be equally accessible by all nodes
				// in the cluster. The CO MUST use the topology information
				// returned by CreateVolumeRequest along with the topology
				// information returned by NodeGetInfo to ensure that a given
				// volume is accessible from a given node when scheduling
				// workloads.
				Type: &csi.PluginCapability_Service_{
					Service: &csi.PluginCapability_Service{
						Type: csi.PluginCapability_Service_VOLUME_ACCESSIBILITY_CONSTRAINTS,
					},
				},
			},
			{
				Type: &csi.PluginCapability_VolumeExpansion_{
					VolumeExpansion: &csi.PluginCapability_VolumeExpansion{
						Type: csi.PluginCapability_VolumeExpansion_ONLINE,
					},
				},
			},
			{
				Type: &csi.PluginCapability_VolumeExpansion_{
					VolumeExpansion: &csi.PluginCapability_VolumeExpansion{
						Type: csi.PluginCapability_VolumeExpansion_OFFLINE,
					},
				},
			},
		},
	}, nil
}

// Probe - return driver status (ready or not)
func (ids *IdentityServer) Probe(ctx context.Context, req *csi.ProbeRequest) (*csi.ProbeResponse, error) {

	ids.log.WithField("func", "Probe()").Infof("request: '%+v'", req)

	// read and validate config (do we need it here?)
	_, err := ids.config.Refresh("")
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "Cannot use config file: %s", err)
	}

	return &csi.ProbeResponse{}, nil
}

// NewIdentityServer - create an instance of identity server
func NewIdentityServer(driver *Driver) *IdentityServer {
	l := driver.log.WithField("cmp", "IdentityServer")
	l.Info("create new IdentityServer...")

	return &IdentityServer{
		config: driver.config,
		log:    l,
	}
}
