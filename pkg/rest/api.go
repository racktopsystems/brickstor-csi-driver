// Copyright 2024 RackTop Systems Inc. and/or its affiliates.
// http://www.racktopsystems.com

package rest

import (
	"strings"
)

func (b *BsrClient) GetPools(req GetPoolsRequest) (GetPoolsResponse, error) {
	var resp GetPoolsResponse
	err := b.Get("/internal/v1/zfs/pools", req, &resp)

	return resp, err
}

func (b *BsrClient) GetDataset(req DatasetRequest) (DatasetResponse, error) {
	var resp DatasetResponse
	err := b.Get("/internal/v1/zfs/dataset", req, &resp)

	return resp, err
}

func (b *BsrClient) GetDatasets(req DatasetsRequest) (DatasetsResponse, error) {
	var resp DatasetsResponse
	err := b.Get("/internal/v1/zfs/datasets", req, &resp)
	if err != nil {
		return resp, err
	}

	// the REST call returns all datasets; filter out "bp" and non-"global"
	var datasets []Dataset
	for _, ds := range resp.Datasets {
		if strings.HasPrefix(ds.Path, "bp") {
			continue
		}

		flds := strings.SplitN(ds.Path, "/", 2)
		if len(flds) != 2 || !strings.HasPrefix(flds[1], "global") {
			continue
		}

		datasets = append(datasets, ds)
	}

	resp.Datasets = datasets

	return resp, nil
}

func (b *BsrClient) CreateDataset(req CreateDatasetRequest) error {
	var resp ErrorResponse
	return b.Post("/internal/v1/dataset/create", req, &resp)
}

func (b *BsrClient) DeleteDataset(req DeleteDatasetRequest) error {
	var resp ErrorResponse
	return b.Post("/internal/v1/dataset/delete", req, &resp)
}

func (b *BsrClient) ModifyDataset(req ModifyDatasetRequest) (ModifyDatasetResponse, error) {
	var resp ModifyDatasetResponse
	err := b.Post("/internal/v1/zfs/dataset/modify", req, &resp)

	return resp, err
}

func (b *BsrClient) FsSetPerms(req FsSetPermissionsRequest) error {
	var resp ErrorResponse
	return b.Post("/internal/v1/fs/perms/apply", req, &resp)
}

func (b *BsrClient) GetSnaps(req GetSnapsRequest) (*GetSnapsResponse, error) {
	var resp GetSnapsResponse
	err := b.Post("/internal/v1/snaps", req, &resp)

	return &resp, err
}

func (b *BsrClient) CreateSnaps(req CreateSnapsRequest) (*SnapsResponse, error) {
	var resp SnapsResponse
	err := b.Post("/internal/v1/snaps/create", req, &resp)

	return &resp, err
}

func (b *BsrClient) DestroySnaps(req DestroySnapsRequest) (*DestroySnapsResponse, error) {
	var resp DestroySnapsResponse
	err := b.Post("/internal/v1/snaps/destroy", req, &resp)

	return &resp, err
}

func (b *BsrClient) Clone(req CloneRequest) (*CloneResponse, error) {
	var resp CloneResponse
	err := b.Post("/internal/v1/dataset/clone", req, &resp)

	return &resp, err
}
