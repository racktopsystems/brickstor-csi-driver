package bsr

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/racktopsystems/brickstor-csi-driver/pkg/rest"
)

// ProviderInterface - BrickStor provider interface
type ProviderInterface interface {
	Addr() string

	LogIn() error

	CreateDataset(path string, refQuotaSize int64) error
	UpdateDataset(path string, refQuotaSize int64) error
	DestroyDataset(path string) error
	SetDatasetACL(path string, readOnly bool) error
	GetDataset(path string) (Dataset, error)
	GetDatasets() ([]Dataset, error)
	GetDsAvailCap(path string) (int64, error)

	CreateNfsShare(path, nfsArgs string) error

	CreateSmbShare(path, shareName string) error
	GetSmbShareName(path string) (string, error)

	CreateSnapshot(path, snapName string) (Snapshot, error)
	DestroySnapshot(path string) error
	GetSnapshot(snapName string) (Snapshot, error)
	GetSnapshots(datasets []string) ([]Snapshot, error)
	CloneSnapshot(snapName, target string, refQuotaSize int64) error
}

// Provider - BrickStor API provider
type Provider struct {
	Address   string // will be a list of addresses for a cluster
	Username  string
	Password  string
	BsrClient *rest.BsrClient
	Log       *logrus.Entry
}

// ProviderArgs - params to create Provider instance
type ProviderArgs struct {
	Address  string
	Username string
	Password string
	Log      *logrus.Entry

	// InsecureSkipVerify controls whether a client verifies the server's
	// certificate chain and host name.
	InsecureSkipVerify bool
}

type Dataset struct {
	Path           string `json:"path"`
	MountPoint     string `json:"mountPoint"`
	SharedOverNfs  bool   `json:"sharedOverNfs"`
	SharedOverSmb  bool   `json:"sharedOverSmb"`
	BytesAvailable int64  `json:"bytesAvailable"`
	BytesUsed      int64  `json:"bytesUsed"`
}

func (p *Provider) Addr() string {
	return p.Address
}

// GetDefaultSmbShareName - all slashes get replaced by underscore
// (converts '/pool/ds1/ds2' to 'pool_ds1_ds2)
func (ds *Dataset) GetDefaultSmbShareName() string {
	return strings.Replace(strings.TrimPrefix(ds.Path, "/"), "/", "_", -1)
}

// GetRefQuotaSize - get total referenced quota size
func (ds *Dataset) GetRefQuotaSize() int64 {
	return ds.BytesAvailable + ds.BytesUsed
}

type Snapshot struct {
	Path         string
	Name         string
	Parent       string
	CreatedBy    string
	Clones       []string
	CreationTime time.Time
}

// newProvider creates a BrickStor provider instance
func newProvider(args ProviderArgs) (*Provider, error) {
	l := args.Log.WithFields(logrus.Fields{
		"cmp":     "BsrProvider",
		"address": args.Address,
	})

	// first do some basic validation of the address

	if args.Address == "" {
		return nil, errors.New("missing BrickStor address")
	}

	if !strings.HasPrefix(args.Address, "https://") {
		return nil, fmt.Errorf("invalid BrickStor address (%s), must be https", args.Address)
	}

	if portIndex := strings.LastIndex(args.Address, ":"); portIndex == -1 {
		return nil, fmt.Errorf("invalid BrickStor address (%s), missing port", args.Address)
	} else {
		portStr := args.Address[portIndex+1:]
		if _, err := strconv.ParseUint(portStr, 10, 16); err != nil {
			return nil, fmt.Errorf("invalid BrickStor address (%s), invalid port (%s)", args.Address, portStr)
		}
	}

	// if necessary, can add additional connection tunables to config and yaml
	opts := rest.BsrClientOpts{
		InsecureSkipVerify: args.InsecureSkipVerify,
	}

	bsrClient := rest.NewBsrClient(args.Address, opts,
		rest.NewBasicAuth(args.Username, args.Password))

	l.Debugf("created provider for '%s'", args.Address)
	return &Provider{
		Address:   args.Address,
		Username:  args.Username,
		Password:  args.Password,
		BsrClient: bsrClient,
		Log:       l,
	}, nil
}

// Helper functions to handle REST errors

func ErrIsAuth(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "invalid credentials")
}

func ErrIsExists(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "already exists")
}

func ErrIsNotExist(err error) bool {
	if err == nil {
		return false
	}
	// "not found" covers both "Dataset not found" and "Pool not found"
	return strings.Contains(err.Error(), "not found") ||
		strings.Contains(err.Error(), "not imported")
}
