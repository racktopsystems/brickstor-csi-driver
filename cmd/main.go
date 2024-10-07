package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"os"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/sirupsen/logrus"

	"github.com/racktopsystems/brickstor-csi-driver/pkg/config"
	"github.com/racktopsystems/brickstor-csi-driver/pkg/driver"
)

// This command is primarily used as the runner for the csi-sanity tests. Some
// point-testing functions are also included for simple standalone testing.

const (
	defaultEndpoint  = "unix:///var/lib/kubelet/plugins_registry/brickstor-csi-driver.racktopsystems.com/csi.sock"
	defaultConfigDir = "/config"
	defaultRole      = driver.RoleAll
)

func main() {
	var (
		nodeID    = flag.String("nodeid", "", "Kubernetes node ID")
		endpoint  = flag.String("endpoint", defaultEndpoint, "CSI endpoint")
		configDir = flag.String("config-dir", defaultConfigDir, "driver config endpoint")
		role      = flag.String("role", "", fmt.Sprintf("driver role: %v", driver.Roles))
		version   = flag.Bool("version", false, "Print driver version")
	)

	flag.Parse()

	if *version {
		fmt.Printf("%s@%s-%s (%s)\n", driver.Name, driver.Version, driver.Commit, driver.DateTime)
		os.Exit(0)
	}

	if nodeID == nil || *nodeID == "" {
		id := "CLI test"
		nodeID = &id
	}

	// init logger
	l := logrus.New().WithFields(logrus.Fields{
		"nodeID": *nodeID,
		"cmp":    "Main",
	})

	l.Info("Run driver with CLI options:")
	l.Infof("- Role:             '%s'", *role)
	l.Infof("- Node ID:          '%s'", *nodeID)
	l.Infof("- CSI endpoint:     '%s'", *endpoint)
	l.Infof("- Config directory: '%s'", *configDir)

	// validate driver instance role
	validatedRole, err := driver.ParseRole(string(*role))
	if err != nil {
		l.Warn(err)
	}

	// initial read and validate config file
	cfg, err := config.New(*configDir)
	if err != nil {
		l.Fatalf("Cannot use config file: %s", err)
	}
	l.Infof("Config file: '%s'", cfg.GetFilePath())

	// logger level
	if cfg.Debug {
		l.Logger.SetLevel(logrus.DebugLevel)
	} else {
		l.Logger.SetLevel(logrus.InfoLevel)
	}

	l.Info("Config file options:")
	for name, config := range cfg.NodeMap {
		l.Infof("  BrickStor cluster: [%+v]", name)
		l.Infof("  - BrickStor address(es): %s", config.Address)
		l.Infof("  - BrickStor username: %s", config.Username)
		l.Infof("  - Default dataset: %s", config.DefaultDataset)
		l.Infof("  - Default data IP: %s", config.DefaultDataIP)
		l.Infof("  - Zone: %s", config.Zone)
		l.Infof("  - InsecureSkipVerify: %+v", *config.InsecureSkipVerify)
	}

	d, err := driver.NewDriver(driver.Args{
		Role:     validatedRole,
		NodeID:   *nodeID,
		Endpoint: *endpoint,
		Config:   cfg,
		Log:      l,
	})
	if err != nil {
		writeTerminationMessage(err, l)
		l.Fatal(err)
	}

	// identityTest(d)

	// controllerTest(d)

	// nodeTest(d)

	// run driver
	err = d.Run()

	if err != nil {
		writeTerminationMessage(err, l)
		l.Fatal(err)
	}
}

// Kubernetes retrieves termination messages from the termination message file of a Container,
// which as a default value of /dev/termination-log
func writeTerminationMessage(err error, l *logrus.Entry) {
	writeErr := ioutil.WriteFile("/dev/termination-log", []byte(fmt.Sprintf("\n%s\n", err)), os.ModePerm)
	if writeErr != nil {
		l.Warnf("Failed to write termination message: %s", writeErr)
	}
}

func identityTest(d *driver.Driver) {
	fmt.Printf("\nidentityTest\n")

	is := driver.NewIdentityServer(d)

	fmt.Printf("\nIdentity.GetPluginInfo\n")
	piReq := &csi.GetPluginInfoRequest{}
	piResp, err := is.GetPluginInfo(context.Background(), piReq)
	if err != nil {
		fmt.Printf("GetPluginInfo err: %s\n", err)
		return
	}

	fmt.Printf("PluginInfo: %s\n", piResp)

	/////////////////////////////

	fmt.Printf("\nIdentity.GetPluginCapabilities\n")
	pcReq := &csi.GetPluginCapabilitiesRequest{}
	pcResp, err := is.GetPluginCapabilities(context.Background(), pcReq)
	if err != nil {
		fmt.Printf("GetPluginCapabilities err: %s\n", err)
		return
	}

	if pcResp.Capabilities != nil {
		for _, cap := range pcResp.Capabilities {
			fmt.Printf("    cap: %+v\n", cap)
		}
	} else {
		fmt.Printf("no capabilities\n")
	}

	/////////////////////////////

	fmt.Printf("\nIdentity.Probe\n")
	pReq := &csi.ProbeRequest{}
	pResp, err := is.Probe(context.Background(), pReq)
	if err != nil {
		fmt.Printf("Probe err: %s\n", err)
		return
	}

	fmt.Printf("probe: %+v\n", pResp)
}

func controllerTest(d *driver.Driver) {
	fmt.Printf("\ncontrollerTest\n")

	cs, err := driver.NewControllerServer(d)
	if err != nil {
		fmt.Printf("NewControllerServer err: %s\n", err)
		return
	}

	fmt.Printf("\nController.ListVolumes\n")
	var startToken string
	for {
		req := &csi.ListVolumesRequest{
			MaxEntries:    100,
			StartingToken: startToken,
		}

		resp, err := cs.ListVolumes(context.Background(), req)
		if err != nil {
			fmt.Printf("ListVolumes err: %s\n", err)
			return
		}

		for _, v := range resp.Entries {
			fmt.Printf("%+v\n", v)
		}
		fmt.Printf("NextToken: %q\n", resp.NextToken)

		if resp.NextToken == "" {
			break
		}
		startToken = resp.NextToken
	}

	// there are tens of thousands of snaps on BrickStor, so only test a
	// couple iterations
	fmt.Printf("\nController.ListSnapshots\n")
	startToken = ""
	for i := 0; i < 2; i++ {
		req := &csi.ListSnapshotsRequest{
			MaxEntries:    10,
			StartingToken: startToken,
		}

		resp, err := cs.ListSnapshots(context.Background(), req)
		if err != nil {
			fmt.Printf("ListSnapshots err: %s\n", err)
			return
		}

		for _, v := range resp.Entries {
			fmt.Printf("%+v\n", v)
		}
		fmt.Printf("NextToken: %q\n", resp.NextToken)

		if resp.NextToken == "" {
			break
		}
		startToken = resp.NextToken
	}

	/////////////////////////////

	fmt.Printf("\nController.CreateVolume\n")
	caps := []*csi.VolumeCapability{
		{
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
			},
		},
	}
	crReq := &csi.CreateVolumeRequest{
		Name:               "csiVolumeDS",
		VolumeCapabilities: caps,
	}

	crResp, err := cs.CreateVolume(context.Background(), crReq)
	if err != nil {
		fmt.Printf("CreateVolume err: %s\n", err)
		return
	}

	vol := crResp.GetVolume()
	fmt.Printf("Created volume: %s\n", vol.VolumeId)

	/////////////////////////////

	fmt.Printf("\nController.CreateSnapshot %q\n", vol.VolumeId)
	snapReq := &csi.CreateSnapshotRequest{
		SourceVolumeId: vol.VolumeId,
		Name:           "foo",
	}

	snapResp, err := cs.CreateSnapshot(context.Background(), snapReq)
	if err != nil {
		fmt.Printf("cs.CreateSnapshot err: %s\n", err)
		return
	}

	fmt.Printf("Created snapshot: %s\n", snapResp.Snapshot.SnapshotId)

	/////////////////////////////

	fmt.Printf("\nController.CreateVolume (from snapshot)\n")
	srcSnap := csi.VolumeContentSource_SnapshotSource{
		SnapshotId: snapResp.Snapshot.SnapshotId,
	}
	volSnap := csi.VolumeContentSource_Snapshot{
		Snapshot: &srcSnap,
	}
	volSrc := csi.VolumeContentSource{
		Type: &volSnap,
	}
	crsReq := &csi.CreateVolumeRequest{
		Name:                "csiVolumeDsSnap",
		VolumeCapabilities:  caps,
		VolumeContentSource: &volSrc,
	}

	crsResp, err := cs.CreateVolume(context.Background(), crsReq)
	if err != nil {
		fmt.Printf("CreateVolume err: %s\n", err)
		return
	}

	snapVol := crsResp.GetVolume()
	fmt.Printf("Created volume (from snapshot): %s\n", snapVol.VolumeId)

	/////////////////////////////

	fmt.Printf("\nController.DeleteVolume %q (from snapshot)\n", snapVol.VolumeId)
	delReq := &csi.DeleteVolumeRequest{
		VolumeId: snapVol.VolumeId,
	}

	_, err = cs.DeleteVolume(context.Background(), delReq)
	if err != nil {
		fmt.Printf("DeleteVolume err: %s\n", err)
		return
	}

	fmt.Printf("Deleted volume: %s\n", snapVol.VolumeId)

	/////////////////////////////

	fmt.Printf("\nController.DeleteSnapshot %q\n", snapResp.Snapshot.SnapshotId)
	delSnapReq := &csi.DeleteSnapshotRequest{
		SnapshotId: snapResp.Snapshot.SnapshotId,
	}

	_, err = cs.DeleteSnapshot(context.Background(), delSnapReq)
	if err != nil {
		fmt.Printf("DeleteSnapshot err: %s\n", err)
		return
	}

	fmt.Printf("Deleted snapshot: %s\n", snapResp.Snapshot.SnapshotId)

	/////////////////////////////

	fmt.Printf("\nController.DeleteVolume %q (original)\n", vol.VolumeId)
	delReq = &csi.DeleteVolumeRequest{
		VolumeId: vol.VolumeId,
	}

	_, err = cs.DeleteVolume(context.Background(), delReq)
	if err != nil {
		fmt.Printf("DeleteVolume err: %s\n", err)
		return
	}

	fmt.Printf("Deleted volume: %s\n", vol.VolumeId)
}

func nodeTest(d *driver.Driver) {
	fmt.Printf("\nnodeTest\n")

	fmt.Printf("driver: %+v\n", *d)

	ns, err := driver.NewNodeServer(d)
	if err != nil {
		fmt.Printf("NewNodeServer err: %s\n", err)
		return
	}

	fmt.Printf("\nNode.GetCapabilities\n")
	capReq := &csi.NodeGetCapabilitiesRequest{}
	capResp, err := ns.NodeGetCapabilities(context.Background(), capReq)
	if err != nil {
		fmt.Printf("GetCapabilities err: %s\n", err)
		return
	}

	if capResp.Capabilities != nil {
		for _, cap := range capResp.Capabilities {
			fmt.Printf("    cap: %+v\n", cap)
		}
	} else {
		fmt.Printf("no capabilities\n")
	}

	/////////////////////////////

	fmt.Printf("\nNode.GetInfo\n")
	infoReq := &csi.NodeGetInfoRequest{}
	infoResp, err := ns.NodeGetInfo(context.Background(), infoReq)
	if err != nil {
		fmt.Printf("GetInfo err: %s\n", err)
		return
	}

	fmt.Printf("info: %+v\n", infoResp)
}
