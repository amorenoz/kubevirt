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

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	vmschema "kubevirt.io/api/core/v1"

	hooksInfo "kubevirt.io/kubevirt/pkg/hooks/info"
	hooksV1alpha2 "kubevirt.io/kubevirt/pkg/hooks/v1alpha2"

	"kubevirt.io/client-go/log"

	"kubevirt.io/network-vhostuser-binding/callback"
	"kubevirt.io/network-vhostuser-binding/domain"
)

type InfoServer struct {
	Version string
}

func (s InfoServer) Info(_ context.Context, _ *hooksInfo.InfoParams) (*hooksInfo.InfoResult, error) {
	return &hooksInfo.InfoResult{
		Name: "network-vhostuser-binding",
		Versions: []string{
			s.Version,
		},
		HookPoints: []*hooksInfo.HookPoint{
			{
				Name:     hooksInfo.OnDefineDomainHookPointName,
				Priority: 0,
			},
		},
	}, nil
}

type V1alpha2Server struct {
	// netInfoOverride, when set, overrides the default downward API network-info
	// file path. Intended for testing only.
	netInfoOverride string
	// socketDirOverride, when set, overrides the default vhost-user socket
	// symlink directory. Intended for testing only.
	socketDirOverride string
}

// SetNetInfoOverride overrides the default downward API network-info file path.
// This is intended for testing.
func (s *V1alpha2Server) SetNetInfoOverride(path string) {
	s.netInfoOverride = path
}

// SetSocketDirOverride overrides the default vhost-user socket symlink directory.
// This is intended for testing.
func (s *V1alpha2Server) SetSocketDirOverride(dir string) {
	s.socketDirOverride = dir
}

func (s V1alpha2Server) OnDefineDomain(_ context.Context, params *hooksV1alpha2.OnDefineDomainParams) (*hooksV1alpha2.OnDefineDomainResult, error) {
	vmi := &vmschema.VirtualMachineInstance{}
	if err := json.Unmarshal(params.GetVmi(), vmi); err != nil {
		return nil, fmt.Errorf("failed to unmarshal VMI: %v", err)
	}

	log.Log.Infof("vhostuser OnDefineDomain")
	log.Log.Infof("VMI: %#v\n", vmi)
	log.Log.Infof("annotations: %#v\n", vmi.GetAnnotations())

	queues := uint(1)
	if mq := vmi.Spec.Domain.Devices.NetworkInterfaceMultiQueue; mq != nil && *mq {
		// We cannot trust the CPU topology of the libvirt domain because the number
		// of sockets gets expanded to make room for future hotplugging. So determine the
		// number of queues from the VMI spec instead.
		if cpuSpec := vmi.Spec.Domain.CPU; cpuSpec != nil {
			cpu := *cpuSpec
			queues = uint(math.Max(float64(cpu.Cores), 1) * math.Max(float64(cpu.Sockets), 1) * math.Max(float64(cpu.Threads), 1))
		}
	}

	useVirtioTransitional := vmi.Spec.Domain.Devices.UseVirtioTransitional != nil && *vmi.Spec.Domain.Devices.UseVirtioTransitional

	opts := domain.VhostUserConfiguratorOptions{
		Queues:                queues,
		UseVirtioTransitional: useVirtioTransitional,
	}
	if len(s.netInfoOverride) > 0 {
		opts.SetNetInfoOverride(s.netInfoOverride)
	}
	if len(s.socketDirOverride) > 0 {
		opts.SetSocketDirOverride(s.socketDirOverride)
	}

	vhostuserConfigurator, err := domain.NewVhostUserNetworkConfigurator(vmi.Spec.Domain.Devices.Interfaces, vmi.Spec.Networks, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create vhostuser configurator: %v", err)
	}

	newDomainXML, err := callback.OnDefineDomain(params.GetDomainXML(), vhostuserConfigurator)
	if err != nil {
		return nil, err
	}

	return &hooksV1alpha2.OnDefineDomainResult{
		DomainXML: newDomainXML,
	}, nil
}

func (s V1alpha2Server) PreCloudInitIso(_ context.Context, params *hooksV1alpha2.PreCloudInitIsoParams) (*hooksV1alpha2.PreCloudInitIsoResult, error) {
	return &hooksV1alpha2.PreCloudInitIsoResult{
		CloudInitData: params.GetCloudInitData(),
	}, nil
}
