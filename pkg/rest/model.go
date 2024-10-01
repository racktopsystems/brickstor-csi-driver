// Copyright 2024 RackTop Systems Inc. and/or its affiliates.
// http://www.racktopsystems.com

package rest

import (
	"time"
)

type Path string

var (
	NeverExpires = time.Unix(253402214400, 0).UTC()
)

type Pool struct {
	Guid             string
	Name             string
	Timestamp        time.Time
	Imported         bool
	DataVdevs        []PoolVdev
	WriteCacheVdevs  []PoolVdev
	ReadCacheDevices []PoolDevice
	SpareDevices     []PoolDevice
	ScanSummary      string
	ScanFunction     string
	ScanState        string
	Errors           string

	// Usage-related fields
	Capacity int
	Size     uint64
	Free     uint64
	Freeing  uint64
	PoolComponent
}

type PoolVdev struct {
	Name    string
	Type    string
	Devices []PoolDevice
	PoolComponent
}

type PoolDevice struct {
	DeviceName string
	Type       string
	ScanStatus string
	Children   []PoolDevice
	PoolComponent
}

type PoolComponent struct {
	Status         string `json:",omitempty"`
	StatusDetail   string `json:",omitempty"` // human readable text
	ReadErrors     int64
	WriteErrors    int64
	ChecksumErrors int64
}

type DatasetProperty struct {
	Name   string
	Value  *string
	Source string // Inherited, Local or System
}

type Dataset struct {
	Id          string // ZFS SUID
	Path        string
	DatasetType string // filesystem or volume
	ParentId    string // ZFS SUID
	Creation    time.Time
	Properties  []DatasetProperty
}

type FSAce struct {
	Sid              string
	PermissionFlags  string
	InheritanceFlags string
	AccessType       string
}

type SnapshotSummary struct {
	Id          string    `json:",omitempty"` // Id of the snapshot
	DatasetPath string    `json:",omitempty"` // DatasetPath of the snapshot
	Name        string    `json:",omitempty"` // Name of the snapshot (random guid)
	Creation    time.Time `json:",omitempty"` // Creation of the snapshot
	CreatedBy   string    `json:",omitempty"` // SID for user-created snap
	Txg         int64     `json:",omitempty"` // Txg of the snapshot
	Alias       string    `json:",omitempty"` // Alias of the snapshot
	Type        string    `json:",omitempty"` // Type of the snapshot
}

// Snapshot represents details of snapshot
type Snapshot struct {
	SnapshotSummary

	// Exp is timestamp of expiration. See NeverExpires for more info.
	Exp time.Time
	// PolicyExp timestamp of expiration based on current policy.
	// This is only applicable to auto created snaps.
	// time.Time{} is reported if current policy is disabled.
	// NeverExpires is reported if current policy has InfiniteRetention.
	PolicyExp time.Time `json:",omitempty"`
	// ReplicaExp specifies timestamp to apply to replica snapshots
	ReplicaExp time.Time
	// PreventRepl specifies whether the snapshot should be skipped during
	// replication.
	PreventRepl bool `json:",omitempty"`

	// ReplPending specifies whether the snapshot is pending replication
	// to one or more replicas.
	ReplPending bool `json:",omitempty"`

	// ReplCommon specifies whether the snapshot is the latest common snapshot
	// between a source and a replica. Each replica can have a different
	// common snapshot if they are not in sync.
	ReplCommon bool `json:",omitempty"`

	Holds  []Hold           `json:",omitempty"` // Holds lists holds
	Clones []DatasetSummary `json:",omitempty"` // Clones lists clones

	Written int64

	Ready bool // Ready for consumption (i.e. indexing, replication)

	AutoDestroy string // AutoDestroy status

	ManifestStatus string `json:",omitempty"`

	Destroyed time.Time `json:",omitempty"` // Destroyed timestamp
	Expired   bool      `json:",omitempty"` // Expired whether destroyed automatically
}

type SnapshotError struct {
	SnapshotSummary
	// Error contains information about the error
	Error string
	// Synopsis contains info about the summary and the error.
	// The summary may not be fully filled if the snapshot does not exist.
	// The synopsis dynamically includes reported fields.
	Synopsis string
}

type DatasetSummary struct {
	Id       string    // Id of the dataset
	Path     string    // Path of the dataset
	Creation time.Time // Creation timestamp of the dataset
}

type Hold struct {
	Creation time.Time // Creation of the hold
	Name     string    // Name of the hold
	// IsUser specifies if hold is user or system
	IsUser bool `json:",omitempty"`
}

// ErrorData contains extra error data returned from http
type ErrorData struct {
	Status  int64
	Code    int64
	ErrType string
	Message string
}

// ErrorResponse contains additional error data from an http request and
// conforms to 'error' interface
type ErrorResponse struct {
	Data ErrorData
}

// Error returns the error string
func (e *ErrorResponse) Error() string {
	return e.Data.Message
}

type GetPoolsRequest struct {
	Pool string // empty (all) or pool guid or pool name
}

type GetPoolsResponse struct {
	Pools []Pool
}

type DatasetRequest struct {
	Dataset string
}

type DatasetsRequest struct {
	Dataset            string
	IncludeAncestors   bool
	ExcludeDescendants bool
	ExcludeVolumes     bool
	ExcludeFilesystems bool
	Props              string
	Limit              int
	Offset             int
}

type DatasetsResponse struct {
	Datasets []Dataset
}

type DatasetResponse struct {
	Dataset Dataset
}

type CreateDatasetRequest struct {
	Name        string
	Properties  []DatasetProperty
	Encrypt     bool
	EncryptAlgo string // if blank will use reasonable default
}

type ModifyDatasetRequest struct {
	Properties []DatasetProperty
	DatasetId  string
}

type ModifyDatasetResponse struct {
	Dataset Dataset
}

type DeleteDatasetRequest struct {
	Dataset string
	Force   bool
}

type FsSetPermissionsRequest struct {
	WaitUntilComplete bool
	DatasetId         string
	Recursive         bool
	Acl               []FSAce
	OwnerSid          string
	OwnerGroupSid     string
	SingleDatasetOnly bool
}

type SnapCriteria struct {
	Datasets    []string // dataset path, snapshot path, dataset suid or snapshot path
	Recursive   bool
	From        time.Time
	To          time.Time
	IncludeTemp bool // Whether to include SnapType Temp snapshots
}

type SnapsRequest struct {
	Criteria SnapCriteria
}

type GetSnapsRequest struct {
	SnapsRequest
	IncludeDestroyed bool
	ExcludeCurrent   bool
}

type SnapsResponse struct {
	Snaps []SnapshotSummary
	BaseSnapsResponse
}

type BaseSnapsResponse struct {
	Errors []SnapshotError
}

type GetSnapsResponse struct {
	Snaps []*Snapshot
	BaseSnapsResponse
}

type DatasetCriteria struct {
	Datasets  []string // dataset path, snapshot path, dataset suid or snapshot path
	Recursive bool
}

type HoldCriteria struct {
	AllUser   bool
	AllSystem bool
	Names     []string
}

type SnapshotHold struct {
	Snap SnapshotSummary // Snap that has the hold
	Hold Hold            // Hold on the snapshot
}

type SnapshotClone struct {
	Snap  SnapshotSummary // Snap is the origin snapshot
	Clone DatasetSummary  // Clone is the clone dataset
}

type CreateSnapsRequest struct {
	DatasetCriteria
	// Name is zfs name of snap. If empty, a random name is used. Alias should
	// be used instead of name.
	Name  string
	Hold  string // Hold to add upon creation
	Alias string // Alias for snap which can include special characters
	// Exp specifies the expiration. A non-zero value is required.
	// Use snap.NeverExpires to prevent expiration.
	Exp time.Time
	// ReplicaExp specifies an alternate replica expiration.
	// If unspecified it defaults to Exp.
	ReplicaExp time.Time
	// PreventRep specifies whether the snap should be excluded from replication
	PreventRepl bool
	// Manifest specifies that a manifest should be created for the snap(s)
	Manifest bool
}

type DestroySnapsRequest struct {
	SnapsRequest
	Release HoldCriteria // Release hold criteria
}

type DestroySnapsResponse struct {
	Clones   []SnapshotClone // Clones that prevent destroy
	Holds    []SnapshotHold  // Holds that prevent destroy
	AllHolds []string        // Distinct set of hold names that prevent destroy
	SnapsResponse
}

type CloneRequest struct {
	// Src specifies the source dataset or snapshot.
	// Id or path is supported.
	// If a dataset is specified, a snapshot will be created.
	Src string
	// Dst specifies the destination dataset path.
	// It has to be on the same pool as the source.
	Dst string
	// Props specifies additional properties to apply to the clone.
	Props map[string]string
}

type CloneResponse struct {
	SnapshotClone
}
