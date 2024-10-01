package driver

import (
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/racktopsystems/brickstor-csi-driver/pkg/bsr"
	"github.com/racktopsystems/brickstor-csi-driver/pkg/config"
)

type VolumeInfo struct {
	ConfigName string
	Path       string
}

func ParseVolumeID(volumeID string) (VolumeInfo, error) {
	splittedParts := strings.Split(volumeID, ":")
	partsLength := len(splittedParts)
	switch partsLength {
	case 2:
		return VolumeInfo{ConfigName: splittedParts[0], Path: splittedParts[1]}, nil
	case 1:
		return VolumeInfo{Path: splittedParts[0]}, nil
	}
	return VolumeInfo{}, status.Error(codes.InvalidArgument, fmt.Sprintf("Unknown VolumeId format: %s", volumeID))
}

func newResolverMap(conf *config.Config, l *logrus.Entry) (map[string]*bsr.Resolver, error) {

	resolverMap := make(map[string]*bsr.Resolver)

	nodes := conf.NodeNames() // get sorted list of names from config

	for _, name := range nodes {
		cfg, ok := conf.LookupNode(name)
		if !ok {
			// driver reconfigured immediately after we got config names
			continue
		}

		bsrResolver, err := bsr.NewResolver(bsr.ResolverArgs{
			Address:            cfg.Address,
			Username:           cfg.Username,
			Password:           cfg.Password,
			Log:                l,
			InsecureSkipVerify: *cfg.InsecureSkipVerify,
		})
		if err != nil {
			return nil, fmt.Errorf("Cannot create BrickStor resolver: %s", err)
		}
		resolverMap[name] = bsrResolver
	}

	return resolverMap, nil
}
