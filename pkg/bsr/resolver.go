package bsr

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"
)

// Resolver - BrickStor storage provider (node or cluster)
type Resolver struct {
	Nodes []*Provider
	Log   *logrus.Entry
}

// Resolve returns a node interface for the BrickStor which is hosting the
// given path (the resolver is either for a standalone node or a cluster).
// For a cluster resolver, this returns the current node on which the dataset
// path resides.
func (r *Resolver) Resolve(path string) (ProviderInterface, error) {
	l := r.Log.WithField("func", "Resolve()")

	if path == "" {
		return nil, fmt.Errorf("Resolve was called with empty pool/dataset path")
	}

	// For a standalone node in the driver yaml config there is a 1:1 mapping
	// from a resolver instance to a bsr node. For a cluster entry in the
	// driver yaml config the resolver instance will have the list of nodes
	// in the cluster.

	var err error
	for _, node := range r.Nodes {
		// make a REST call to the node to get the named dataset (this tells
		// us if that node is the "provider" of that dataset)
		if _, err = node.GetDataset(path); err == nil {
			l.Debugf("resolved '%s' to '%s'", path, node)
			return node, nil
		}
	}

	l.Debugf("no BrickStor found with dataset: '%s'", path)
	return nil, err
}

func (r *Resolver) ResolveByAddress(addr string) (ProviderInterface, error) {
	l := r.Log.WithField("func", "ResolveByAddr()")

	if addr == "" {
		return nil, errors.New("ResolveByAddr called with empty address")
	}

	for _, provider := range r.Nodes {
		if provider.Address == addr {
			l.Debugf("resolved '%s'", addr)
			return provider, nil
		}
	}

	l.Debugf("no BrickStor found with address: '%s'", addr)
	return nil, fmt.Errorf("provider for address %s not found", addr)
}

// ResolverArgs - params to create resolver instance from config
type ResolverArgs struct {
	Address  string
	Username string
	Password string
	Log      *logrus.Entry

	// InsecureSkipVerify controls whether a client verifies the server's
	// certificate chain and host name.
	InsecureSkipVerify bool
}

// NewResolver creates a resolver instance based on the configuration
func NewResolver(args ResolverArgs) (*Resolver, error) {
	l := args.Log.WithFields(logrus.Fields{
		"cmp":     "BsrResolver",
		"address": args.Address,
	})

	if args.Address == "" {
		return nil, errors.New("BrickStor address(es) not specified")
	}

	var nodes []*Provider
	addresses := strings.Split(args.Address, ",")

	// If len(addressList) > 1, then the nodes are in the same cluster,
	// otherwise this is a standalone node. Each resolver represents either
	// a standalone node or a cluster.

	for _, address := range addresses {
		bsrNode, err := newProvider(ProviderArgs{
			Address:            address,
			Username:           args.Username,
			Password:           args.Password,
			Log:                l,
			InsecureSkipVerify: args.InsecureSkipVerify,
		})
		if err != nil {
			return nil, fmt.Errorf("Cannot create provider for %s BrickStor: %s", address, err)
		}
		nodes = append(nodes, bsrNode)
	}

	l.Debugf("resolver created for '%s'", args.Address)
	return &Resolver{
		Nodes: nodes,
		Log:   l,
	}, nil
}
