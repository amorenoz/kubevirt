/*
 * This file is part of the KubeVirt project
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * Copyright 2025 Red Hat, Inc.
 *
 */

package domain

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strconv"
	"time"

	networkv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"

	vmschema "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/log"

	"kubevirt.io/kubevirt/pkg/network/downwardapi"
	"kubevirt.io/kubevirt/pkg/network/namescheme"
	domainschema "kubevirt.io/kubevirt/pkg/virt-launcher/virtwrap/api"
	"kubevirt.io/kubevirt/pkg/virt-launcher/virtwrap/device"
	"kubevirt.io/network-vhostuser-binding/dra"
)

type vhostNetworkData struct {
	socketPath string
	mtu        int
}

type VhostUserNetworkConfigurator struct {
	vhostIfaces []*vmschema.Interface
	opts        VhostUserConfiguratorOptions
	networkData map[string]vhostNetworkData // data extracted from Downward API
}

type VhostUserConfiguratorOptions struct {
	Queues                uint
	UseVirtioTransitional bool
	// netInfoOverride, when set, overrides the default downward API network-info
	// file path. Intended for testing only.
	netInfoOverride string
	// socketDirOverride, when set, overrides VhostUserSocketDir.
	// Intended for testing only.
	socketDirOverride string
	// draBaseDirOverride, when set, overrides the default DRA metadata
	// directory [dra.ContainerDir]. Intended for testing only.
	draBaseDirOverride string
}

// SetNetInfoOverride overrides the default downward API network-info file path.
// This is intended for testing; production code should leave it unset.
func (o *VhostUserConfiguratorOptions) SetNetInfoOverride(path string) {
	o.netInfoOverride = path
}

// SetSocketDirOverride overrides the default vhost-user socket symlink directory.
// This is intended for testing.
func (o *VhostUserConfiguratorOptions) SetSocketDirOverride(dir string) {
	o.socketDirOverride = dir
}

// SetDRABaseDirOverride overrides the default DRA metadata directory.
// This is intended for testing; production code should leave it unset.
func (o *VhostUserConfiguratorOptions) SetDRABaseDirOverride(dir string) {
	o.draBaseDirOverride = dir
}

const (
	// VhostUserPluginName vhost-user binding plugin name should be registered to Kubevirt through Kubevirt CR
	VhostUserPluginName = "vhostuser"
	// VhostUserLogFilePath vhost-user log file path Kubevirt consume and record
	VhostUserLogFilePath = "/var/run/kubevirt/vhost-user.log"
	// Directory where the binding will symlink sockets.
	VhostUserSocketDir = "/var/run/kubevirt-hooks/"
	// QueueSize is the TX/RX queue size for vhost-user interfaces.
	QueueSize uint32 = 1024
)

func NewVhostUserNetworkConfigurator(ifaces []vmschema.Interface, networks []vmschema.Network, opts VhostUserConfiguratorOptions) (*VhostUserNetworkConfigurator, error) {

	vhostIfaces := make([]*vmschema.Interface, 0)
	for _, iface := range ifaces {
		if iface.Binding != nil && iface.Binding.Name == VhostUserPluginName {
			vhostIfaces = append(vhostIfaces, &iface)
		}
	}

	if len(vhostIfaces) == 0 {
		return nil, fmt.Errorf("no vhost interfaces found")
	}

	networkData, err := resolveNetworkData(vhostIfaces, networks, opts)
	if err != nil {
		return nil, err
	}

	return &VhostUserNetworkConfigurator{
		vhostIfaces: vhostIfaces,
		opts:        opts,
		networkData: networkData,
	}, nil
}

// resolveNetworkData determines the socket path for each vhost-user interface.
// It prefers DRA (KEP-5304) when a reader is available, falling back to the
// downward API otherwise.
func resolveNetworkData(vhostIfaces []*vmschema.Interface, networks []vmschema.Network, opts VhostUserConfiguratorOptions) (map[string]vhostNetworkData, error) {
	baseDir := opts.draBaseDirOverride
	if baseDir == "" {
		baseDir = dra.ContainerDir
	}

	if _, err := os.Stat(baseDir); err == nil {
		log.Log.Infof("DRA: metadata directory %q present, attempting DRA discovery", baseDir)
		// Map VMI logical network names to their hashed pod interface names,
		// which is what the DRA claim is named after.
		podIfaceNames := namescheme.CreateHashedNetworkNameScheme(networks)
		data, err := readDRANetworkData(vhostIfaces, podIfaceNames, dra.Reader{BaseDir: baseDir})
		if err == nil {
			log.Log.Infof("DRA: socket discovery succeeded for all interfaces")
			return data, nil
		}
		log.Log.Warningf("DRA: socket discovery failed (%v), falling back to downward API", err)
	} else {
		log.Log.Infof("DRA: metadata directory %q absent (%v), using downward API", baseDir, err)
	}

	netInfoPath := path.Join(downwardapi.MountPath, downwardapi.NetworkInfoVolumePath)
	if opts.netInfoOverride != "" {
		log.Log.Infof("downward API: overriding netInfoPath with %q", opts.netInfoOverride)
		netInfoPath = opts.netInfoOverride
	}
	data, err := readVhostNetworkData(netInfoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read network info: %w", err)
	}
	return data, nil
}

// readDRANetworkData resolves the socket path for every vhost-user interface
// via DRA metadata. Returns an error if any interface cannot be resolved so
// that the caller can fall back to the downward API.
// podIfaceNames maps VMI logical network name → hashed pod interface name,
// which is the name the DRA claim is registered under.
func readDRANetworkData(vhostIfaces []*vmschema.Interface, podIfaceNames map[string]string, reader dra.Reader) (map[string]vhostNetworkData, error) {
	result := make(map[string]vhostNetworkData, len(vhostIfaces))
	for _, iface := range vhostIfaces {
		podIfaceName, ok := podIfaceNames[iface.Name]
		if !ok {
			return nil, fmt.Errorf("interface %q: no pod interface name found", iface.Name)
		}
		log.Log.Infof("DRA: interface=%q maps to pod interface name %q", iface.Name, podIfaceName)
		meta, resourceNameUsed, err := dra.ResolveForInterface(reader, podIfaceName)
		if err != nil {
			return nil, fmt.Errorf("interface %q (pod iface %q): %w", iface.Name, podIfaceName, err)
		}
		log.Log.Infof("DRA: interface=%q resolved via requestName=%q socketPath=%q",
			iface.Name, resourceNameUsed, meta.SocketPath)
		result[iface.Name] = vhostNetworkData{socketPath: meta.SocketPath}
	}
	return result, nil
}

func (p VhostUserNetworkConfigurator) Mutate(domainSpec *domainschema.DomainSpec) (*domainschema.DomainSpec, error) {
	domainSpecCopy := domainSpec.DeepCopy()

	for _, vhostIface := range p.vhostIfaces {
		log.Log.Infof("%s: generating domain interface definition. queues = %d", vhostIface.Name, p.opts.Queues)
		generatedIface, err := p.generateDomainInterface(vhostIface)
		if err != nil {
			return nil, fmt.Errorf("%s: failed to generate domain interface spec for iface: %v", vhostIface.Name, err)
		}
		log.Log.Infof("%s: generated domain interface definition: %+v", vhostIface.Name, generatedIface)

		if iface := lookupIfaceByAliasName(domainSpecCopy.Devices.Interfaces, vhostIface.Name); iface != nil {
			*iface = *generatedIface
		} else {
			domainSpecCopy.Devices.Interfaces = append(domainSpecCopy.Devices.Interfaces, *generatedIface)
		}
	}

	if mb := domainSpecCopy.MemoryBacking; mb != nil {
		if access := mb.Access; access != nil {
			if access.Mode != "shared" {
				log.Log.Warningf("vduse vhostuser requires memoryBacking access to be shared but it's %s. Overwriting", access)
				access.Mode = "shared"
			}
		} else {
			mb.Access = &domainschema.MemoryBackingAccess{
				Mode: "shared",
			}
		}
	} else {
		domainSpecCopy.MemoryBacking = &domainschema.MemoryBacking{
			Access: &domainschema.MemoryBackingAccess{
				Mode: "shared",
			},
		}
	}

	domainSpecCopy.MemoryBacking.Access.Mode = "shared"

	return domainSpecCopy, nil
}

func (p VhostUserNetworkConfigurator) getVhostUserPath(iface *vmschema.Interface) (string, error) {
	data, ok := p.networkData[iface.Name]
	if !ok {
		return "", fmt.Errorf("vhost user path for interface %s not found", iface.Name)
	}
	sockFile := path.Base(data.socketPath)
	sockDir := path.Dir(data.socketPath)

	// Vhost-user socket paths might contain deployment-specific strings if they come from a DevicePlugin.
	// These bits would change when another pod is deployed for the same VM, i.e: during live-migration, leading
	// to a different libvirt XML. In order to avoid this, symlink the sockets to well-known paths that will
	// remain stable across migrations.
	socketDir := VhostUserSocketDir
	if p.opts.socketDirOverride != "" {
		socketDir = p.opts.socketDirOverride
	}

	if err := os.MkdirAll(socketDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create vhost-user socket directory: %w", err)
	}

	symlinkPath := path.Join(socketDir, iface.Name)
	if _, err := os.Lstat(symlinkPath); err == nil {
		log.Log.Warningf("Removing existing symlink for interface %s at %s", iface.Name, symlinkPath)
		if err := os.Remove(symlinkPath); err != nil {
			return "", fmt.Errorf("failed to remove existing symlink: %w", err)
		}
	}

	if err := os.Symlink(sockDir, symlinkPath); err != nil {
		return "", fmt.Errorf("failed to create symlink for socket: %w", err)
	}

	// We get a volume's subpath based on the CONTAINER_NAME while the launcher container
	// gets the full volume. Append CONTAINER_NAME to the path we tell libvirt to use.
	if env := os.Getenv("CONTAINER_NAME"); env != "" {
		symlinkPath = path.Join(socketDir, env, iface.Name)
	}

	return path.Join(symlinkPath, sockFile), nil
}

func (p VhostUserNetworkConfigurator) generateDomainInterface(iface *vmschema.Interface) (*domainschema.Interface, error) {
	var pciAddress *domainschema.Address
	if iface.PciAddress != "" {
		var err error
		pciAddress, err = device.NewPciAddressField(iface.PciAddress)
		if err != nil {
			return nil, err
		}
	}

	var ifaceModelType string
	if p.opts.UseVirtioTransitional {
		ifaceModelType = "virtio-transitional"
	} else {
		ifaceModelType = "virtio"
	}
	model := &domainschema.Model{Type: ifaceModelType}

	var mac *domainschema.MAC
	if iface.MacAddress != "" {
		mac = &domainschema.MAC{MAC: iface.MacAddress}
	}

	var acpi *domainschema.ACPI
	if iface.ACPIIndex > 0 {
		acpi = &domainschema.ACPI{Index: uint(iface.ACPIIndex)}
	}

	//vhostUserPath, err := p.getVhostUserPath(iface)
	data, ok := p.networkData[iface.Name]
	if !ok {
		return nil, fmt.Errorf("No network data for interface %s", iface.Name)
	}
	vhostUserPath := data.socketPath
	queueSize := uint(QueueSize)

	var mtu *domainschema.MTU
	if data, ok := p.networkData[iface.Name]; ok && data.mtu > 0 {
		mtu = &domainschema.MTU{Size: strconv.Itoa(data.mtu)}
	}

	domIface := &domainschema.Interface{
		Alias:   domainschema.NewUserDefinedAlias(iface.Name),
		Model:   model,
		Address: pciAddress,
		MAC:     mac,
		ACPI:    acpi,
		Type:    "vhostuser",
		Source:  domainschema.InterfaceSource{Type: "unix", Path: vhostUserPath, Mode: "server"},
		Driver:  &domainschema.InterfaceDriver{TXQueueSize: &queueSize, RXQueueSize: &queueSize, Queues: &p.opts.Queues},
		MTU:     mtu,
	}

	return domIface, nil
}

func lookupIfaceByAliasName(ifaces []domainschema.Interface, name string) *domainschema.Interface {
	for i, iface := range ifaces {
		if iface.Alias != nil && iface.Alias.GetName() == name {
			return &ifaces[i]
		}
	}

	return nil
}

func readFileWithTimeout(path string, timeout uint32) ([]byte, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	timeoutChan := time.After(time.Duration(timeout) * time.Second)

	// Try reading immediately first
	data, err := os.ReadFile(path)
	if err == nil {
		return data, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}

	for {
		select {
		case <-timeoutChan:
			return nil, fmt.Errorf("timeout reading file %s after %d seconds", path, timeout)
		case <-ticker.C:
			data, err := os.ReadFile(path)
			if err == nil {
				return data, nil
			}
			if !os.IsNotExist(err) {
				return nil, err
			}
		}
	}
}

func readVhostNetworkData(netInfoPath string) (map[string]vhostNetworkData, error) {
	data, err := readFileWithTimeout(netInfoPath, 5)
	if err != nil {
		return nil, fmt.Errorf("failed to read network info file: %w", err)
	}

	var networkInfo downwardapi.NetworkInfo
	if err := json.Unmarshal(data, &networkInfo); err != nil {
		return nil, fmt.Errorf("failed to unmarshal NetworkInfo: %w", err)
	}

	result := make(map[string]vhostNetworkData, len(networkInfo.Interfaces))
	for _, iface := range networkInfo.Interfaces {
		if iface.DeviceInfo == nil || iface.DeviceInfo.Type != networkv1.DeviceInfoTypeVHostUser {
			continue
		}
		if iface.DeviceInfo.VhostUser == nil || iface.DeviceInfo.VhostUser.Mode != networkv1.VhostDeviceModeClient {
			return nil, fmt.Errorf("deviceInfo for interface %s has wrong vhost-user mode (expected %s)",
				iface.Network, networkv1.VhostDeviceModeClient)
		}
		if iface.DeviceInfo.VhostUser.Path == "" {
			return nil, fmt.Errorf("deviceInfo for interface %s has empty vhost-user socket path",
				iface.Network)
		}
		result[iface.Network] = vhostNetworkData{
			socketPath: iface.DeviceInfo.VhostUser.Path,
			mtu:        iface.Mtu,
		}
	}
	return result, nil
}
