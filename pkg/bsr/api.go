package bsr

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/racktopsystems/brickstor-csi-driver/pkg/rest"
)

// LogIn logs in to a BrickStor Server
func (p *Provider) LogIn() error {
	l := p.Log.WithField("func", "LogIn()")

	if err := p.BsrClient.Login(); err != nil {
		l.Errorf("login to BrickStor %s failed (username: '%s'), "+
			"make sure to use correct address and password",
			p.Address, p.Username)
		return err
	}

	l.Debugf("login successful")
	return nil
}

// GetDsAvailCap returns dataset's available space (in bytes)
func (p *Provider) GetDsAvailCap(path string) (int64, error) {

	dsReq := rest.DatasetsRequest{
		Dataset:            path,
		ExcludeDescendants: true,
		ExcludeVolumes:     true,
		Props:              "available",
	}

	resp, err := p.BsrClient.GetDatasets(dsReq)
	if err != nil {
		return 0, err
	}

	var available int64
	// should only be one dataset in resp
	for _, d := range resp.Datasets {
		for _, p := range d.Properties {
			if p.Name == "available" {
				val, _ := strconv.ParseUint(*p.Value, 10, 64)
				available = int64(val)
			}
		}
	}

	return available, nil
}

func (p *Provider) GetDataset(path string) (Dataset, error) {
	ds := Dataset{Path: path}

	if path == "" {
		return ds, fmt.Errorf("path is empty")
	}

	dsReq := rest.DatasetsRequest{
		Dataset:            path,
		ExcludeDescendants: true,
		ExcludeVolumes:     true,
		Props:              "available,used,mountpoint,sharenfs,sharesmb",
	}

	resp, err := p.BsrClient.GetDatasets(dsReq)
	if err != nil {
		return ds, err
	}

	// should only be one dataset in resp
	for _, d := range resp.Datasets {
		for _, p := range d.Properties {
			if p.Name == "available" {
				val, _ := strconv.ParseUint(*p.Value, 10, 64)
				ds.BytesAvailable = int64(val)
			} else if p.Name == "used" {
				val, _ := strconv.ParseUint(*p.Value, 10, 64)
				ds.BytesUsed = int64(val)
			} else if p.Name == "mountpoint" {
				ds.MountPoint = *p.Value
			} else if p.Name == "sharenfs" {
				if *p.Value != "-" && *p.Value != "off" {
					ds.SharedOverNfs = true
				}
			} else if p.Name == "sharesmb" {
				if *p.Value != "-" && *p.Value != "off" {
					ds.SharedOverSmb = true
				}
			}
		}
	}

	return ds, nil
}

// GetDatasets returns all usable datasets (non-"bp" and "{pool}/global").
func (p *Provider) GetDatasets() ([]Dataset, error) {

	dsReq := rest.DatasetsRequest{
		ExcludeDescendants: false,
		ExcludeVolumes:     true,
		Props:              "available,used,mountpoint,sharenfs,sharesmb",
	}

	resp, err := p.BsrClient.GetDatasets(dsReq)
	if err != nil {
		return nil, err
	}

	var datasets []Dataset
	for _, d := range resp.Datasets {

		ds := Dataset{Path: d.Path}

		for _, p := range d.Properties {
			if p.Name == "available" {
				val, _ := strconv.ParseUint(*p.Value, 10, 64)
				ds.BytesAvailable = int64(val)
			} else if p.Name == "used" {
				val, _ := strconv.ParseUint(*p.Value, 10, 64)
				ds.BytesUsed = int64(val)
			} else if p.Name == "mountpoint" {
				ds.MountPoint = *p.Value
			} else if p.Name == "sharenfs" {
				if len(*p.Value) > 0 {
					ds.SharedOverNfs = true
				}
			} else if p.Name == "sharesmb" {
				if len(*p.Value) > 0 {
					ds.SharedOverSmb = true
				}
			}
		}

		datasets = append(datasets, ds)
	}

	return datasets, nil
}

func (p *Provider) CreateDataset(path string, refQuotaSize int64) error {
	if path == "" {
		return errors.New("CreateFilesystem path is required")
	}

	crReq := rest.CreateDatasetRequest{
		Name: path,
	}

	if refQuotaSize > 0 {
		quotaStr := strconv.FormatInt(refQuotaSize, 10)
		crReq.Properties = []rest.DatasetProperty{
			rest.DatasetProperty{
				Name:  "refquota",
				Value: &quotaStr,
			},
		}
	}

	return p.BsrClient.CreateDataset(crReq)
}

func (p *Provider) UpdateDataset(path string, refQuotaSize int64) error {
	if path == "" {
		return fmt.Errorf("Parameter 'path' is required")
	}

	dsReq := rest.DatasetRequest{Dataset: path}
	resp, err := p.BsrClient.GetDataset(dsReq)
	if err != nil {
		return err
	}

	dsId := resp.Dataset.Id
	var strPtr *string // nil pointer removes the setting
	if refQuotaSize > 0 {
		quotaStr := strconv.FormatInt(refQuotaSize, 10)
		strPtr = &quotaStr
	}
	modReq := rest.ModifyDatasetRequest{
		DatasetId: dsId,
		Properties: []rest.DatasetProperty{
			rest.DatasetProperty{
				Name:  "refquota",
				Value: strPtr,
			},
		},
	}

	_, err = p.BsrClient.ModifyDataset(modReq)
	return err
}

// DestroyFilesystem deletes the dataset on the BrickStor. snapd properly
// handles dataset deletion even when there is a clone of a snap on the dataset.
func (p *Provider) DestroyDataset(path string) error {

	if path == "" {
		return fmt.Errorf("Filesystem path is required")
	}

	delReq := rest.DeleteDatasetRequest{
		Dataset: path,
		Force:   false,
	}

	return p.BsrClient.DeleteDataset(delReq)
}

// CreateNfsShare creates an NFS share on the specified dataset
func (p *Provider) CreateNfsShare(path, nfsArgs string) error {
	if path == "" {
		return fmt.Errorf("CreateNfsShareParams path is required")
	}

	dsReq := rest.DatasetRequest{Dataset: path}
	resp, err := p.BsrClient.GetDataset(dsReq)
	if err != nil {
		return err
	}

	dsId := resp.Dataset.Id
	val := "anon=0,sec=sys"
	if nfsArgs != "" {
		val = fmt.Sprintf("%s,%s", val, nfsArgs)
	}
	modReq := rest.ModifyDatasetRequest{
		DatasetId: dsId,
		Properties: []rest.DatasetProperty{
			rest.DatasetProperty{
				Name:  "sharenfs",
				Value: &val,
			},
		},
	}

	_, err = p.BsrClient.ModifyDataset(modReq)
	return err
}

// CreateSmbShare creates an SMB share (cifs) on the specified dataset
// Leave shareName empty to generate default value
func (p *Provider) CreateSmbShare(path, shareName string) error {
	if path == "" {
		return fmt.Errorf("CreateSmbShare path is required")
	}

	dsReq := rest.DatasetRequest{Dataset: path}
	resp, err := p.BsrClient.GetDataset(dsReq)
	if err != nil {
		return err
	}

	dsId := resp.Dataset.Id
	val := "on"
	if shareName != "" {
		val = fmt.Sprintf("name=%s", shareName)
	}
	modReq := rest.ModifyDatasetRequest{
		DatasetId: dsId,
		Properties: []rest.DatasetProperty{
			rest.DatasetProperty{
				Name:  "sharesmb",
				Value: &val,
			},
		},
	}

	_, err = p.BsrClient.ModifyDataset(modReq)
	return err
}

// GetSmbShareName returns share name for dataset that is shared over SMB
func (p *Provider) GetSmbShareName(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("Filesystem path is required")
	}

	dsReq := rest.DatasetsRequest{
		Dataset:            path,
		ExcludeDescendants: true,
		ExcludeVolumes:     true,
		Props:              "sharesmb",
	}

	resp, err := p.BsrClient.GetDatasets(dsReq)
	if err != nil {
		return "", err
	}

	// should only be one dataset in resp
	for _, d := range resp.Datasets {
		for _, p := range d.Properties {
			if p.Name == "sharesmb" {
				val := *p.Value
				if len(val) > 0 {
					if val != "on" {
						options := strings.Split(val, ",")
						for _, o := range options {
							if strings.HasPrefix(o, "name=") {
								return o[5:], nil
							}
						}
					}

					// return the default share name
					return path, nil
				}
			}
		}
	}

	return "", fmt.Errorf("%s is not an smb share", path)
}

// SetDatasetACL sets the ACL so an NFS share allows a user to write without
// checking the UNIX uid.
func (p *Provider) SetDatasetACL(path string, readOnly bool) error {
	if path == "" {
		return fmt.Errorf("Filesystem path is required")
	}

	dsReq := rest.DatasetRequest{Dataset: path}
	resp, err := p.BsrClient.GetDataset(dsReq)
	if err != nil {
		return err
	}

	dsId := resp.Dataset.Id
	fsAce := rest.FSAce{
		Sid:              "everyone@",
		PermissionFlags:  "rwxpdDaARWcCos", // default r/w
		InheritanceFlags: "fd-----",
		AccessType:       "Allow",
	}

	if readOnly {
		fsAce.PermissionFlags = "r-x---a-R-c---"
	}

	acls := []rest.FSAce{fsAce}

	fsReq := rest.FsSetPermissionsRequest{
		WaitUntilComplete: true,
		DatasetId:         dsId,
		Recursive:         false,
		Acl:               acls,
		SingleDatasetOnly: true,
	}

	return p.BsrClient.FsSetPerms(fsReq)
}

// CreateSnapshot creates snapshot with the given name for the specified dataset
func (p *Provider) CreateSnapshot(path, snapName string) (Snapshot, error) {

	snap := Snapshot{
		Path: path,
		Name: snapName,
	}

	if path == "" {
		return snap, fmt.Errorf("Parameter 'CreateSnapshot path' is required")
	}

	crSnapReq := rest.CreateSnapsRequest{
		DatasetCriteria: rest.DatasetCriteria{
			Datasets:  []string{path},
			Recursive: false,
		},
		Name: snapName,
		Exp:  rest.NeverExpires,
	}
	resp, err := p.BsrClient.CreateSnaps(crSnapReq)

	if err != nil {
		return snap, err
	}

	// this should be a paranoid check
	if len(resp.Snaps) != 1 {
		return snap, errors.New("snapshot was not created")
	}

	s := resp.Snaps[0]

	snap.CreationTime = s.Creation

	return snap, nil
}

// GetSnapshot returns snapshot with given name
func (p *Provider) GetSnapshot(snapName string) (Snapshot, error) {
	snap := Snapshot{}

	if snapName == "" {
		return snap, errors.New("snapshot name is empty")
	}

	splitName := strings.Split(snapName, "@")
	if len(splitName) != 2 {
		return snap, errors.New("invalid snapshot name")
	}
	dsName := splitName[0]

	snapReq := rest.GetSnapsRequest{
		SnapsRequest: rest.SnapsRequest{
			Criteria: rest.SnapCriteria{
				Datasets:  []string{snapName},
				Recursive: false,
			},
		},
	}

	resp, err := p.BsrClient.GetSnaps(snapReq)
	if err != nil {
		return snap, err
	}

	if len(resp.Snaps) != 1 {
		return snap, errors.New("snapshot does not exist")
	}

	s := resp.Snaps[0]

	// get the parent dataset name by stripping the last component
	var parent string
	leafIndex := strings.LastIndex(dsName, "/")
	if leafIndex != -1 {
		parent = dsName[0:leafIndex]
	}

	snap.Path = dsName
	snap.Name = s.Name
	snap.Parent = parent
	snap.CreatedBy = s.CreatedBy
	snap.CreationTime = s.Creation

	return snap, nil
}

// GetSnapshots returns snapshots by dataset name
func (p *Provider) GetSnapshots(datasets []string) ([]Snapshot, error) {

	var snaps []Snapshot

	if len(datasets) == 0 {
		return snaps, errors.New("get snapshots dataset list is empty")
	}

	snapReq := rest.GetSnapsRequest{
		SnapsRequest: rest.SnapsRequest{
			Criteria: rest.SnapCriteria{
				Datasets:  datasets,
				Recursive: false, // only get snaps for named datasets
			},
		},
	}

	resp, err := p.BsrClient.GetSnaps(snapReq)
	if err != nil {
		return snaps, err
	}

	for _, s := range resp.Snaps {
		dsName := s.DatasetPath

		// get the parent dataset name by stripping the last component
		var parent string
		leafIndex := strings.LastIndex(dsName, "/")
		if leafIndex != -1 {
			parent = dsName[0:leafIndex]
		}

		snap := Snapshot{
			Path:         dsName,
			Name:         s.Name,
			Parent:       parent,
			CreatedBy:    s.CreatedBy,
			CreationTime: s.Creation,
		}

		snaps = append(snaps, snap)
	}

	return snaps, nil
}

// DestroySnapshot destroys snapshot by path
func (p *Provider) DestroySnapshot(path string) error {
	if path == "" {
		return errors.New("destroy snapshot path is required")
	}

	destSnapReq := rest.DestroySnapsRequest{
		SnapsRequest: rest.SnapsRequest{
			Criteria: rest.SnapCriteria{
				Datasets: []string{path},
			},
		},
	}

	_, err := p.BsrClient.DestroySnaps(destSnapReq)
	return err
}

// CloneSnapshot clones snapshot to new dataset
func (p *Provider) CloneSnapshot(snapName, target string, refQuotaSize int64) error {
	if snapName == "" {
		return errors.New("clone snapshot name is required")
	}

	if target == "" {
		return errors.New("clone target path is required")
	}

	cloneReq := rest.CloneRequest{
		Src: snapName,
		Dst: target,
	}

	if refQuotaSize > 0 {
		props := make(map[string]string)
		props["refquota"] = strconv.FormatInt(refQuotaSize, 10)
		cloneReq.Props = props
	}

	_, err := p.BsrClient.Clone(cloneReq)
	return err
}
