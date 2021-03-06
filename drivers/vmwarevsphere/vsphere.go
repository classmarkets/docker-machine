/*
 * Copyright 2014 VMware, Inc.  All rights reserved.  Licensed under the Apache v2 License.
 */

package vmwarevsphere

import (
	"archive/tar"
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/classmarkets/docker-machine/libmachine/drivers"
	"github.com/classmarkets/docker-machine/libmachine/log"
	"github.com/classmarkets/docker-machine/libmachine/mcnflag"
	"github.com/classmarkets/docker-machine/libmachine/mcnutils"
	"github.com/classmarkets/docker-machine/libmachine/ssh"
	"github.com/classmarkets/docker-machine/libmachine/state"

	"errors"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/guest"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"
	"golang.org/x/net/context"
)

const (
	// dockerBridgeIP is the default IP address of the docker0 bridge.
	dockerBridgeIP = "172.17.0.1"
	isoFilename    = "boot2docker.iso"
	// B2DUser is the guest User for tools login
	B2DUser = "docker"
	// B2DPass is the guest Pass for tools login
	B2DPass = "tcuser"
)

type Driver struct {
	*drivers.BaseDriver
	Memory         int
	DiskSize       int
	CPU            int
	ISO            string
	Boot2DockerURL string
	CPUS           int

	IP         string
	Port       int
	Username   string
	Password   string
	Network    string
	Networks   []string
	Datastore  string
	Datacenter string
	Folder     string
	Pool       string
	HostSystem string
	CfgParams  []string
	CloudInit  string

	SSHPassword string
}

const (
	defaultSSHUser  = B2DUser
	defaultSSHPass  = B2DPass
	defaultCpus     = 2
	defaultMemory   = 2048
	defaultDiskSize = 20480
	defaultSDKPort  = 443
)

// GetCreateFlags registers the flags this driver adds to
// "docker-machine create"
func (d *Driver) GetCreateFlags() []mcnflag.Flag {
	return []mcnflag.Flag{
		mcnflag.IntFlag{
			EnvVar: "VSPHERE_CPU_COUNT",
			Name:   "vmwarevsphere-cpu-count",
			Usage:  "vSphere CPU number for docker VM",
			Value:  defaultCpus,
		},
		mcnflag.IntFlag{
			EnvVar: "VSPHERE_MEMORY_SIZE",
			Name:   "vmwarevsphere-memory-size",
			Usage:  "vSphere size of memory for docker VM (in MB)",
			Value:  defaultMemory,
		},
		mcnflag.IntFlag{
			EnvVar: "VSPHERE_DISK_SIZE",
			Name:   "vmwarevsphere-disk-size",
			Usage:  "vSphere size of disk for docker VM (in MB)",
			Value:  defaultDiskSize,
		},
		mcnflag.StringFlag{
			EnvVar: "VSPHERE_BOOT2DOCKER_URL",
			Name:   "vmwarevsphere-boot2docker-url",
			Usage:  "vSphere URL for boot2docker image",
		},
		mcnflag.StringFlag{
			EnvVar: "VSPHERE_VCENTER",
			Name:   "vmwarevsphere-vcenter",
			Usage:  "vSphere IP/hostname for vCenter",
		},
		mcnflag.IntFlag{
			EnvVar: "VSPHERE_VCENTER_PORT",
			Name:   "vmwarevsphere-vcenter-port",
			Usage:  "vSphere Port for vCenter",
			Value:  defaultSDKPort,
		},
		mcnflag.StringFlag{
			EnvVar: "VSPHERE_USERNAME",
			Name:   "vmwarevsphere-username",
			Usage:  "vSphere username",
		},
		mcnflag.StringFlag{
			EnvVar: "VSPHERE_PASSWORD",
			Name:   "vmwarevsphere-password",
			Usage:  "vSphere password",
		},
		mcnflag.StringSliceFlag{
			EnvVar: "VSPHERE_NETWORK",
			Name:   "vmwarevsphere-network",
			Usage:  "vSphere network where the docker VM will be attached",
		},
		mcnflag.StringFlag{
			EnvVar: "VSPHERE_DATASTORE",
			Name:   "vmwarevsphere-datastore",
			Usage:  "vSphere datastore for docker VM",
		},
		mcnflag.StringFlag{
			EnvVar: "VSPHERE_DATACENTER",
			Name:   "vmwarevsphere-datacenter",
			Usage:  "vSphere datacenter for docker VM",
		},
		mcnflag.StringFlag{
			EnvVar: "VSPHERE_FOLDER",
			Name:   "vmwarevsphere-folder",
			Usage:  "vSphere folder for the docker VM. This folder must already exist in the datacenter.",
		},
		mcnflag.StringFlag{
			EnvVar: "VSPHERE_POOL",
			Name:   "vmwarevsphere-pool",
			Usage:  "vSphere resource pool for docker VM",
		},
		mcnflag.StringFlag{
			EnvVar: "VSPHERE_HOSTSYSTEM",
			Name:   "vmwarevsphere-hostsystem",
			Usage:  "vSphere compute resource where the docker VM will be instantiated. This can be omitted if using a cluster with DRS.",
		},
		mcnflag.StringSliceFlag{
			EnvVar: "VSPHERE_CFGPARAM",
			Name:   "vmwarevsphere-cfgparam",
			Usage:  "vSphere vm configuration parameters (used for guestinfo)",
		},
		mcnflag.StringFlag{
			EnvVar: "VSPHERE_CLOUDINIT",
			Name:   "vmwarevsphere-cloudinit",
			Usage:  "vSphere cloud-init file or url to set in the guestinfo",
		},
	}
}

func NewDriver(hostName, storePath string) drivers.Driver {
	return &Driver{
		CPUS:        defaultCpus,
		Memory:      defaultMemory,
		DiskSize:    defaultDiskSize,
		SSHPassword: defaultSSHPass,
		Port:        defaultSDKPort,
		BaseDriver: &drivers.BaseDriver{
			SSHUser:     defaultSSHUser,
			MachineName: hostName,
			StorePath:   storePath,
		},
	}
}

func (d *Driver) GetSSHHostname() (string, error) {
	return d.GetIP()
}

func (d *Driver) GetSSHUsername() string {
	if d.SSHUser == "" {
		d.SSHUser = "docker"
	}

	return d.SSHUser
}

// DriverName returns the name of the driver
func (d *Driver) DriverName() string {
	return "vmwarevsphere"
}

func (d *Driver) SetConfigFromFlags(flags drivers.DriverOptions) error {
	d.SSHUser = "docker"
	d.SSHPort = 22
	d.CPU = flags.Int("vmwarevsphere-cpu-count")
	d.Memory = flags.Int("vmwarevsphere-memory-size")
	d.DiskSize = flags.Int("vmwarevsphere-disk-size")
	d.Boot2DockerURL = flags.String("vmwarevsphere-boot2docker-url")
	d.IP = flags.String("vmwarevsphere-vcenter")
	d.Port = flags.Int("vmwarevsphere-vcenter-port")
	d.Username = flags.String("vmwarevsphere-username")
	d.Password = flags.String("vmwarevsphere-password")
	d.Networks = flags.StringSlice("vmwarevsphere-network")
	d.Datastore = flags.String("vmwarevsphere-datastore")
	d.Datacenter = flags.String("vmwarevsphere-datacenter")
	// Sanitize input on ingress.
	d.Folder = strings.Trim(flags.String("vmwarevsphere-folder"), "/")
	d.Pool = flags.String("vmwarevsphere-pool")
	d.HostSystem = flags.String("vmwarevsphere-hostsystem")
	d.CfgParams = flags.StringSlice("vmwarevsphere-cfgparam")
	d.CloudInit = flags.String("vmwarevsphere-cloudinit")
	d.SetSwarmConfigFromFlags(flags)

	d.ISO = d.ResolveStorePath(isoFilename)

	return nil
}

func (d *Driver) GetURL() (string, error) {

	ip, err := d.GetIP()
	if err != nil {
		return "", err
	}
	if ip == "" {
		return "", nil
	}
	return fmt.Sprintf("tcp://%s", net.JoinHostPort(ip, "2376")), nil
}

func (d *Driver) GetIP() (string, error) {
	status, err := d.GetState()
	if status != state.Running {
		return "", drivers.ErrHostIsNotRunning
	}

	// Create context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c, err := d.vsphereLogin(ctx)
	if err != nil {
		return "", err
	}
	defer c.Logout(ctx)

	vm, err := d.fetchVM(ctx, c, d.MachineName)
	if err != nil {
		return "", err
	}

	configuredMacIPs, err := vm.WaitForNetIP(ctx, false)
	if err != nil {
		return "", err
	}

	for _, ips := range configuredMacIPs {
		if len(ips) >= 0 {
			// Prefer IPv4 address, but fall back to first/IPv6
			preferredIP := ips[0]
			for _, ip := range ips {
				// In addition to non IPv4 addresses, try to filter
				// out link local addresses and the default address of
				// the Docker0 bridge
				netIP := net.ParseIP(ip)
				if netIP.To4() != nil && netIP.IsGlobalUnicast() && !netIP.Equal(net.ParseIP(dockerBridgeIP)) {
					preferredIP = ip
					break
				}
			}
			return preferredIP, nil
		}
	}

	return "", errors.New("No IP despite waiting for one - check DHCP status")
}

func (d *Driver) GetState() (state.State, error) {

	// Create context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c, err := d.vsphereLogin(ctx)
	if err != nil {
		return state.None, err
	}
	defer c.Logout(ctx)

	vm, err := d.fetchVM(ctx, c, d.MachineName)
	if err != nil {
		return state.None, err
	}

	var mvm mo.VirtualMachine

	err = c.RetrieveOne(ctx, vm.Reference(), nil, &mvm)
	if err != nil {
		return state.None, nil
	}

	s := mvm.Summary

	if strings.Contains(string(s.Runtime.PowerState), "poweredOn") {
		return state.Running, nil
	} else if strings.Contains(string(s.Runtime.PowerState), "poweredOff") {
		return state.Stopped, nil
	}
	return state.None, nil
}

// PreCreateCheck checks that the machine creation process can be started safely.
func (d *Driver) PreCreateCheck() error {
	log.Debug("Connecting to vSphere for pre-create checks...")
	// Create context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c, err := d.vsphereLogin(ctx)
	if err != nil {
		return err
	}
	defer c.Logout(ctx)

	// Create a new finder
	f := find.NewFinder(c.Client, true)

	dc, err := f.DatacenterOrDefault(ctx, d.Datacenter)
	if err != nil {
		return err
	}

	f.SetDatacenter(dc)

	// Folder
	if d.Folder != "" {
		// Find the specified Folder to create the VM in.
		folders, err := dc.Folders(ctx)
		if err != nil {
			return err
		}
		folder, err := f.Folder(ctx, fmt.Sprintf("%s/%s", folders.VmFolder.InventoryPath, d.Folder))
		// It's an error to not find the folder, or for the search itself to fail.
		if err != nil {
			// The search itself failed.
			return err
		}
		if folder == nil {
			return fmt.Errorf("failed to find VM Folder '%s'", d.Folder)
		}
	}

	if _, err := f.DatastoreOrDefault(ctx, d.Datastore); err != nil {
		return err
	}

	// TODO: if the user has both the VSPHERE_NETWORK defined and adds --vmwarevsphere-network
	//       both are used at the same time - probably should detect that and remove the one from ENV
	if len(d.Networks) == 0 {
		// machine assumes there will be a network
		d.Networks = append(d.Networks, "VM Network")
	}
	for _, netName := range d.Networks {
		if _, err := f.NetworkOrDefault(ctx, netName); err != nil {
			return err
		}
	}
	// d.Network needs to remain a string to cope with existing machines :/
	d.Network = d.Networks[0]

	var hs *object.HostSystem
	if d.HostSystem != "" {
		var err error
		hs, err = f.HostSystemOrDefault(ctx, d.HostSystem)
		if err != nil {
			return err
		}
	}

	// ResourcePool
	if d.Pool != "" {
		// Find specified Resource Pool
		if _, err := f.ResourcePool(ctx, d.Pool); err != nil {
			return err
		}
	} else if hs != nil {
		// Pick default Resource Pool for Host System
		if _, err := hs.ResourcePool(ctx); err != nil {
			return err
		}
	} else {
		// Pick the default Resource Pool for the Datacenter.
		if _, err := f.DefaultResourcePool(ctx); err != nil {
			return err
		}
	}

	return nil
}

// Create has the following implementation:
// 1. check whether the docker directory contains the boot2docker ISO
// 2. generate an SSH keypair and bundle it in a tar.
// 3. create a virtual machine with the boot2docker ISO mounted;
// 4. reconfigure the virtual machine network and disk size;
func (d *Driver) Create() error {
	b2dutils := mcnutils.NewB2dUtils(d.StorePath)
	if err := b2dutils.CopyIsoToMachineDir(d.Boot2DockerURL, d.MachineName); err != nil {
		return err
	}

	log.Infof("Generating SSH Keypair...")
	if err := ssh.GenerateSSHKey(d.GetSSHKeyPath()); err != nil {
		return err
	}

	// Create context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c, err := d.vsphereLogin(ctx)
	if err != nil {
		return err
	}
	defer c.Logout(ctx)

	// Create a new finder
	f := find.NewFinder(c.Client, true)

	dc, err := f.DatacenterOrDefault(ctx, d.Datacenter)
	if err != nil {
		return err
	}

	f.SetDatacenter(dc)

	dss, err := f.DatastoreOrDefault(ctx, d.Datastore)
	if err != nil {
		return err
	}

	networks := make(map[string]object.NetworkReference)
	for _, netName := range d.Networks {
		net, err := f.NetworkOrDefault(ctx, netName)
		if err != nil {
			return err
		}
		networks[netName] = net
	}

	var hs *object.HostSystem
	if d.HostSystem != "" {
		var err error
		hs, err = f.HostSystemOrDefault(ctx, d.HostSystem)
		if err != nil {
			return err
		}
	}

	var rp *object.ResourcePool
	if d.Pool != "" {
		// Find specified Resource Pool
		rp, err = f.ResourcePool(ctx, d.Pool)
		if err != nil {
			return err
		}
	} else if d.HostSystem != "" {
		// Pick default Resource Pool for Host System
		rp, err = hs.ResourcePool(ctx)
		if err != nil {
			return err
		}
	} else {
		// Pick the default Resource Pool for the Datacenter.
		rp, err = f.DefaultResourcePool(ctx)
		if err != nil {
			return err
		}
	}

	spec := types.VirtualMachineConfigSpec{
		Name:     d.MachineName,
		GuestId:  "otherLinux64Guest",
		Files:    &types.VirtualMachineFileInfo{VmPathName: fmt.Sprintf("[%s]", dss.Name())},
		NumCPUs:  int32(d.CPU),
		MemoryMB: int64(d.Memory),
	}

	scsi, err := object.SCSIControllerTypes().CreateSCSIController("pvscsi")
	if err != nil {
		return err
	}

	spec.DeviceChange = append(spec.DeviceChange, &types.VirtualDeviceConfigSpec{
		Operation: types.VirtualDeviceConfigSpecOperationAdd,
		Device:    scsi,
	})

	log.Infof("Creating VM...")
	folders, err := dc.Folders(ctx)
	folder := folders.VmFolder
	if d.Folder != "" {
		folder, err = f.Folder(ctx, fmt.Sprintf("%s/%s", folders.VmFolder.InventoryPath, d.Folder))
		if err != nil {
			return err
		}
	}
	task, err := folder.CreateVM(ctx, spec, rp, hs)
	if err != nil {
		return err
	}

	info, err := task.WaitForResult(ctx, nil)
	if err != nil {
		return err
	}

	log.Infof("Uploading Boot2docker ISO ...")
	dsurl, err := dss.URL(ctx, dc, fmt.Sprintf("%s/%s", d.MachineName, isoFilename))
	if err != nil {
		return err
	}
	p := soap.DefaultUpload
	if err = c.Client.UploadFile(d.ISO, dsurl, &p); err != nil {
		return err
	}

	// Retrieve the new VM
	vm := object.NewVirtualMachine(c.Client, info.Result.(types.ManagedObjectReference))

	devices, err := vm.Device(ctx)
	if err != nil {
		return err
	}

	var add []types.BaseVirtualDevice

	controller, err := devices.FindDiskController("scsi")
	if err != nil {
		return err
	}

	disk := devices.CreateDisk(controller, dss.Reference(),
		dss.Path(fmt.Sprintf("%s/%s.vmdk", d.MachineName, d.MachineName)))

	// Convert MB to KB
	disk.CapacityInKB = int64(d.DiskSize) * 1024

	add = append(add, disk)
	ide, err := devices.FindIDEController("")
	if err != nil {
		return err
	}

	cdrom, err := devices.CreateCdrom(ide)
	if err != nil {
		return err
	}

	add = append(add, devices.InsertIso(cdrom, dss.Path(fmt.Sprintf("%s/%s", d.MachineName, isoFilename))))

	for _, netName := range d.Networks {
		backing, err := networks[netName].EthernetCardBackingInfo(ctx)
		if err != nil {
			return err
		}

		netdev, err := object.EthernetCardTypes().CreateEthernetCard("vmxnet3", backing)
		if err != nil {
			return err
		}

		log.Infof("adding network: %s", netName)
		add = append(add, netdev)
	}

	log.Infof("Reconfiguring VM")
	if vm.AddDevice(ctx, add...); err != nil {
		return err
	}

	// Adding some guestinfo data
	var opts []types.BaseOptionValue
	for _, param := range d.CfgParams {
		v := strings.SplitN(param, "=", 2)
		key := v[0]
		value := ""
		if len(v) > 1 {
			value = v[1]
		}
		fmt.Printf("Setting %s to %s\n", key, value)
		opts = append(opts, &types.OptionValue{
			Key:   key,
			Value: value,
		})
	}
	if d.CloudInit != "" {
		if _, err := url.ParseRequestURI(d.CloudInit); err == nil {
			log.Infof("setting guestinfo.cloud-init.data.url to %s\n", d.CloudInit)
			opts = append(opts, &types.OptionValue{
				Key:   "guestinfo.cloud-init.config.url",
				Value: d.CloudInit,
			})
		} else {
			if _, err := os.Stat(d.CloudInit); err == nil {
				if value, err := ioutil.ReadFile(d.CloudInit); err == nil {
					log.Infof("setting guestinfo.cloud-init.data to encoded content of %s\n", d.CloudInit)
					encoded := base64.StdEncoding.EncodeToString(value)
					opts = append(opts, &types.OptionValue{
						Key:   "guestinfo.cloud-init.config.data",
						Value: encoded,
					})
					opts = append(opts, &types.OptionValue{
						Key:   "guestinfo.cloud-init.data.encoding",
						Value: "base64",
					})
				}
			}
		}
	}

	task, err = vm.Reconfigure(ctx, types.VirtualMachineConfigSpec{
		ExtraConfig: opts,
	})
	if err != nil {
		return err
	}
	task.Wait(ctx)

	if err := d.Start(); err != nil {
		return err
	}

	log.Infof("Provisioning certs and ssh keys...")
	// Generate a tar keys bundle
	if err := d.generateKeyBundle(); err != nil {
		return err
	}

	opman := guest.NewOperationsManager(c.Client, vm.Reference())

	fileman, err := opman.FileManager(ctx)
	if err != nil {
		return err
	}

	src := d.ResolveStorePath("userdata.tar")
	s, err := os.Stat(src)
	if err != nil {
		return err
	}

	auth := AuthFlag{}
	flag := FileAttrFlag{}
	auth.auth.Username = B2DUser
	auth.auth.Password = B2DPass
	flag.SetPerms(0, 0, 660)
	url, err := fileman.InitiateFileTransferToGuest(ctx, auth.Auth(), "/home/docker/userdata.tar", flag.Attr(), s.Size(), true)
	if err != nil {
		return err
	}
	u, err := c.Client.ParseURL(url)
	if err != nil {
		return err
	}
	if err = c.Client.UploadFile(src, u, nil); err != nil {
		return err
	}

	procman, err := opman.ProcessManager(ctx)
	if err != nil {
		return err
	}

	// first, untar - only boot2docker has /var/lib/boot2docker
	// TODO: don't hard-code to docker & staff - they are also just b2d
	var env []string
	guestspec := types.GuestProgramSpec{
		ProgramPath:      "/usr/bin/sudo",
		Arguments:        "/usr/bin/sudo /bin/sh -c \"tar xvf /home/docker/userdata.tar -C /home/docker > /var/log/userdata.log 2>&1 && chown -R docker:staff /home/docker\"",
		WorkingDirectory: "",
		EnvVariables:     env,
	}

	_, err = procman.StartProgram(ctx, auth.Auth(), &guestspec)
	if err != nil {
		return err
	}

	// now move to /var/lib/boot2docker if its there
	guestspec = types.GuestProgramSpec{
		ProgramPath:      "/usr/bin/sudo",
		Arguments:        "/bin/mv /home/docker/userdata.tar /var/lib/boot2docker/userdata.tar",
		WorkingDirectory: "",
		EnvVariables:     env,
	}

	_, err = procman.StartProgram(ctx, auth.Auth(), &guestspec)
	if err != nil {
		return err
	}

	return nil
}

func (d *Driver) Start() error {
	machineState, err := d.GetState()
	if err != nil {
		return err
	}

	switch machineState {
	case state.Running:
		log.Infof("VM %s has already been started", d.MachineName)
		return nil
	case state.Stopped:
		// TODO add transactional or error handling in the following steps
		// Create context
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		c, err := d.vsphereLogin(ctx)
		if err != nil {
			return err
		}
		defer c.Logout(ctx)

		vm, err := d.fetchVM(ctx, c, d.MachineName)
		if err != nil {
			return err
		}

		task, err := vm.PowerOn(ctx)
		if err != nil {
			return err
		}

		_, err = task.WaitForResult(ctx, nil)
		if err != nil {
			return err
		}

		log.Infof("Waiting for VMware Tools to come online...")
		if d.IPAddress, err = d.GetIP(); err != nil {
			return err
		}
	}
	return nil
}

func (d *Driver) Stop() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c, err := d.vsphereLogin(ctx)
	if err != nil {
		return err
	}
	defer c.Logout(ctx)

	vm, err := d.fetchVM(ctx, c, d.MachineName)
	if err != nil {
		return err
	}

	if err := vm.ShutdownGuest(ctx); err != nil {
		return err
	}

	d.IPAddress = ""

	return nil
}

func (d *Driver) Restart() error {
	if err := d.Stop(); err != nil {
		return err
	}

	// Check for 120 seconds for the machine to stop
	for i := 1; i <= 60; i++ {
		machineState, err := d.GetState()
		if err != nil {
			return err
		}
		if machineState == state.Running {
			log.Debugf("Not there yet %d/%d", i, 60)
			time.Sleep(2 * time.Second)
			continue
		}
		if machineState == state.Stopped {
			break
		}
	}

	machineState, err := d.GetState()
	// If the VM is still running after 120 seconds just kill it.
	if machineState == state.Running {
		if err = d.Kill(); err != nil {
			return fmt.Errorf("can't stop VM: %s", err)
		}
	}

	return d.Start()
}

func (d *Driver) Kill() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c, err := d.vsphereLogin(ctx)
	if err != nil {
		return err
	}
	defer c.Logout(ctx)

	vm, err := d.fetchVM(ctx, c, d.MachineName)
	if err != nil {
		return err
	}

	task, err := vm.PowerOff(ctx)
	if err != nil {
		return err
	}

	_, err = task.WaitForResult(ctx, nil)
	if err != nil {
		return err
	}

	d.IPAddress = ""

	return nil
}

func (d *Driver) Remove() error {
	machineState, err := d.GetState()
	if err != nil {
		return err
	}
	if machineState == state.Running {
		if err = d.Kill(); err != nil {
			return fmt.Errorf("can't stop VM: %s", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c, err := d.vsphereLogin(ctx)
	if err != nil {
		return err
	}
	defer c.Logout(ctx)

	// Create a new finder
	f := find.NewFinder(c.Client, true)

	dc, err := f.DatacenterOrDefault(ctx, d.Datacenter)
	if err != nil {
		return err
	}

	f.SetDatacenter(dc)

	dss, err := f.DatastoreOrDefault(ctx, d.Datastore)
	if err != nil {
		return err
	}

	// Remove B2D Iso from VM folder
	m := object.NewFileManager(c.Client)
	task, err := m.DeleteDatastoreFile(ctx, dss.Path(fmt.Sprintf("%s/%s", d.MachineName, isoFilename)), dc)
	if err != nil {
		return err
	}

	err = task.Wait(ctx)
	if err != nil {
		if types.IsFileNotFound(err) {
			// Ignore error
			return nil
		}
	}

	vm, err := d.fetchVM(ctx, c, d.MachineName)
	if err != nil {
		return err
	}

	task, err = vm.Destroy(ctx)
	if err != nil {
		return err
	}

	_, err = task.WaitForResult(ctx, nil)
	if err != nil {
		return err
	}
	return nil
}

func (d *Driver) Upgrade() error {
	return fmt.Errorf("upgrade is not supported for vsphere driver at this moment")
}

func (d *Driver) publicSSHKeyPath() string {
	return d.GetSSHKeyPath() + ".pub"
}

// Make a boot2docker userdata.tar key bundle
func (d *Driver) generateKeyBundle() error {
	log.Debugf("Creating Tar key bundle...")

	magicString := "boot2docker, this is vmware speaking"

	tf, err := os.Create(d.ResolveStorePath("userdata.tar"))
	if err != nil {
		return err
	}
	defer tf.Close()
	var fileWriter = tf

	tw := tar.NewWriter(fileWriter)
	defer tw.Close()

	// magicString first so we can figure out who originally wrote the tar.
	file := &tar.Header{Name: magicString, Size: int64(len(magicString))}
	if err := tw.WriteHeader(file); err != nil {
		return err
	}
	if _, err := tw.Write([]byte(magicString)); err != nil {
		return err
	}
	// .ssh/key.pub => authorized_keys
	file = &tar.Header{Name: ".ssh", Typeflag: tar.TypeDir, Mode: 0700}
	if err := tw.WriteHeader(file); err != nil {
		return err
	}
	pubKey, err := ioutil.ReadFile(d.publicSSHKeyPath())
	if err != nil {
		return err
	}
	file = &tar.Header{Name: ".ssh/authorized_keys", Size: int64(len(pubKey)), Mode: 0644}
	if err := tw.WriteHeader(file); err != nil {
		return err
	}
	if _, err := tw.Write([]byte(pubKey)); err != nil {
		return err
	}
	file = &tar.Header{Name: ".ssh/authorized_keys2", Size: int64(len(pubKey)), Mode: 0644}
	if err := tw.WriteHeader(file); err != nil {
		return err
	}
	if _, err := tw.Write([]byte(pubKey)); err != nil {
		return err
	}
	err = tw.Close()
	return err
}

func (d *Driver) vsphereLogin(ctx context.Context) (*govmomi.Client, error) {

	// Parse URL from string
	u, err := url.Parse(fmt.Sprintf("https://%s:%d/sdk", d.IP, d.Port))
	if err != nil {
		return nil, err
	}
	// set username and password for the URL
	u.User = url.UserPassword(d.Username, d.Password)

	// Connect and log in to ESX or vCenter
	c, err := govmomi.NewClient(ctx, u, true)
	if err != nil {
		return nil, err
	}

	return c, nil
}

func (d *Driver) fetchVM(ctx context.Context, c *govmomi.Client, vmname string) (*object.VirtualMachine, error) {

	// Create a new finder
	f := find.NewFinder(c.Client, true)

	var vm *object.VirtualMachine
	var err error

	dc, err := f.DatacenterOrDefault(ctx, d.Datacenter)
	if err != nil {
		return vm, err
	}

	f.SetDatacenter(dc)

	vmPath := vmname
	if d.Folder != "" {
		vmPath = fmt.Sprintf("%s/%s", d.Folder, vmname)
	}
	vm, err = f.VirtualMachine(ctx, vmPath)
	if err != nil {
		return vm, err
	}
	return vm, nil
}

type AuthFlag struct {
	auth types.NamePasswordAuthentication
}

func (f *AuthFlag) Auth() types.BaseGuestAuthentication {
	return &f.auth
}

type FileAttrFlag struct {
	types.GuestPosixFileAttributes
}

func (f *FileAttrFlag) SetPerms(owner, group, perms int) {
	f.OwnerId = int32(owner)
	f.GroupId = int32(group)
	f.Permissions = int64(perms)
}

func (f *FileAttrFlag) Attr() types.BaseGuestFileAttributes {
	return &f.GuestPosixFileAttributes
}
