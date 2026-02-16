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
	"crypto/sha256"
	"fmt"
	"io"
	"path"

	vmschema "kubevirt.io/api/core/v1"

	domainschema "kubevirt.io/kubevirt/pkg/virt-launcher/virtwrap/api"

	"kubevirt.io/client-go/log"

	"kubevirt.io/kubevirt/pkg/virt-launcher/virtwrap/device"
)

type VhostUserNetworkConfigurator struct {
	vhostIfaces []*vmschema.Interface
	opts        VhostUserConfiguratorOptions
}

type VhostUserConfiguratorOptions struct {
	Queues                uint
	UseVirtioTransitional bool
}

const (
	// VhostUserPluginName vhost-user binding plugin name should be registered to Kubevirt through Kubevirt CR
	VhostUserPluginName = "vhostuser"
	// VhostUserLogFilePath vhost-user log file path Kubevirt consume and record
	VhostUserLogFilePath = "/var/run/kubevirt/vhost-user.log"
	// VhostUserLogFilePath path where vhost user sockets will be placed
	// HACK! we should really find a way to get a host mount properly specified
	VhostUserSockPath        = "/var/lib/vhost_sockets"
	QueueSize         uint32 = 1024
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

	return &VhostUserNetworkConfigurator{
		vhostIfaces: vhostIfaces,
		opts:        opts,
	}, nil
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

func (p VhostUserNetworkConfigurator) getVhostUserPath(iface *vmschema.Interface) string {
	// FIXME. Kubevirt generates this as interface name!
	hash := sha256.New()
	_, _ = io.WriteString(hash, iface.Name)
	hashedName := fmt.Sprintf("%x", hash.Sum(nil))[:11]
	return path.Join(VhostUserSockPath, fmt.Sprintf("%s%s", "pod", hashedName))
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

	vhostUserPath := p.getVhostUserPath(iface)
	queueSize := uint(QueueSize)

	return &domainschema.Interface{
		Alias:   domainschema.NewUserDefinedAlias(iface.Name),
		Model:   model,
		Address: pciAddress,
		MAC:     mac,
		ACPI:    acpi,
		Type:    "vhostuser",
		Source:  domainschema.InterfaceSource{Type: "unix", Path: vhostUserPath, Mode: "server"},
		Driver:  &domainschema.InterfaceDriver{TXQueueSize: &queueSize, RXQueueSize: &queueSize, Queues: &p.opts.Queues},
	}, nil
}

func lookupIfaceByAliasName(ifaces []domainschema.Interface, name string) *domainschema.Interface {
	for i, iface := range ifaces {
		if iface.Alias != nil && iface.Alias.GetName() == name {
			return &ifaces[i]
		}
	}

	return nil
}
