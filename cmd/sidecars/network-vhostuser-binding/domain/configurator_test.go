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

package domain_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	vmschema "kubevirt.io/api/core/v1"

	"kubevirt.io/kubevirt/cmd/sidecars/network-vhostuser-binding/domain"

	domainschema "kubevirt.io/kubevirt/pkg/virt-launcher/virtwrap/api"
)

var _ = Describe("vhostuser network configurator", func() {
	const testPodId = "test-pod-uid-12345"

	Context("generate domain spec interface", func() {
		DescribeTable("should fail to create configurator given",
			func(ifaces []vmschema.Interface, networks []vmschema.Network) {
				_, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, testPodId)

				Expect(err).To(HaveOccurred())
			},
			Entry("no interfaces",
				nil,
				[]vmschema.Network{{Name: "default", NetworkSource: vmschema.NetworkSource{Multus: &vmschema.MultusNetwork{}}}},
			),
			Entry("interface with no vhostuser binding method",
				[]vmschema.Interface{{Name: "default", InterfaceBindingMethod: vmschema.InterfaceBindingMethod{Bridge: &vmschema.InterfaceBridge{}}}},
				[]vmschema.Network{*vmschema.DefaultPodNetwork()},
			),
			Entry("interface with no vhostuser binding plugin",
				[]vmschema.Interface{{Name: "default", Binding: &vmschema.PluginBinding{Name: "no-vhostuser"}}},
				[]vmschema.Network{*vmschema.DefaultPodNetwork()},
			),
		)

		It("should fail given interface with invalid PCI address", func() {
			ifaces := []vmschema.Interface{{Name: "default", Binding: &vmschema.PluginBinding{Name: "vhostuser"},
				PciAddress: "invalid-pci-address"}}
			networks := []vmschema.Network{*vmschema.DefaultPodNetwork()}

			testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, testPodId)
			Expect(err).ToNot(HaveOccurred())

			_, err = testMutator.Mutate(&domainschema.DomainSpec{})
			Expect(err).To(HaveOccurred())
		})

		DescribeTable("should add interface to domain spec given iface with",
			func(iface *vmschema.Interface, expectedDomainIface *domainschema.Interface) {
				ifaces := []vmschema.Interface{*iface}
				networks := []vmschema.Network{*vmschema.DefaultPodNetwork()}

				testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, testPodId)
				Expect(err).ToNot(HaveOccurred())

				mutatedDomSpec, err := testMutator.Mutate(&domainschema.DomainSpec{})
				Expect(err).ToNot(HaveOccurred())
				Expect(mutatedDomSpec.Devices.Interfaces).To(Equal([]domainschema.Interface{*expectedDomainIface}))
			},
			Entry("vhostuser binding plugin",
				&vmschema.Interface{Name: "default", Binding: &vmschema.PluginBinding{Name: "vhostuser"}},
				&domainschema.Interface{
					Alias:  domainschema.NewUserDefinedAlias("default"),
					Type:   "vhostuser",
					Source: domainschema.InterfaceSource{Type: "unix", Path: "/var/run/kubevirt-private/vmi-disks/vhost-user/test/default.sock", Mode: "server"},
					Model:  &domainschema.Model{Type: "virtio"},
				},
			),
			Entry("PCI address",
				&vmschema.Interface{Name: "default", Binding: &vmschema.PluginBinding{Name: "vhostuser"},
					PciAddress: "0000:02:02.0"},
				&domainschema.Interface{
					Alias:   domainschema.NewUserDefinedAlias("default"),
					Type:    "vhostuser",
					Source:  domainschema.InterfaceSource{Type: "unix", Path: "/var/run/kubevirt-private/vmi-disks/vhost-user/test/default.sock", Mode: "server"},
					Model:   &domainschema.Model{Type: "virtio"},
					Address: &domainschema.Address{Type: "pci", Domain: "0x0000", Bus: "0x02", Slot: "0x02", Function: "0x0"},
				},
			),
			Entry("MAC address",
				&vmschema.Interface{Name: "default", Binding: &vmschema.PluginBinding{Name: "vhostuser"},
					MacAddress: "02:02:02:02:02:02"},
				&domainschema.Interface{
					Alias:  domainschema.NewUserDefinedAlias("default"),
					Type:   "vhostuser",
					Source: domainschema.InterfaceSource{Type: "unix", Path: "/var/run/kubevirt-private/vmi-disks/vhost-user/test/default.sock", Mode: "server"},
					Model:  &domainschema.Model{Type: "virtio"},
					MAC:    &domainschema.MAC{MAC: "02:02:02:02:02:02"},
				},
			),
			Entry("ACPI address",
				&vmschema.Interface{Name: "default", Binding: &vmschema.PluginBinding{Name: "vhostuser"},
					ACPIIndex: 2},
				&domainschema.Interface{
					Alias:  domainschema.NewUserDefinedAlias("default"),
					Type:   "vhostuser",
					Source: domainschema.InterfaceSource{Type: "unix", Path: "/var/run/kubevirt-private/vmi-disks/vhost-user/test/default.sock", Mode: "server"},
					Model:  &domainschema.Model{Type: "virtio"},
					ACPI:   &domainschema.ACPI{Index: uint(2)},
				},
			),
		)

		It("should not override other interfaces", func() {
			networks := []vmschema.Network{
				*vmschema.DefaultPodNetwork(),
				{Name: "secondary", NetworkSource: vmschema.NetworkSource{Multus: &vmschema.MultusNetwork{NetworkName: "sec"}}},
			}
			ifaces := []vmschema.Interface{
				{Name: "default", Binding: &vmschema.PluginBinding{Name: "vhostuser"}},
				{Name: "secondary", InterfaceBindingMethod: vmschema.InterfaceBindingMethod{Bridge: &vmschema.InterfaceBridge{}}},
			}

			expectedDomainIface := &domainschema.Interface{
				Alias:  domainschema.NewUserDefinedAlias("default"),
				Type:   "vhostuser",
				Source: domainschema.InterfaceSource{Type: "unix", Path: "/var/run/kubevirt-private/vmi-disks/vhost-user/test/default.sock", Mode: "server"},
				Model:  &domainschema.Model{Type: "virtio"},
			}

			testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, testPodId)
			Expect(err).ToNot(HaveOccurred())

			existingIface := &domainschema.Interface{Alias: domainschema.NewUserDefinedAlias("existing-iface")}
			testDomSpec := &domainschema.DomainSpec{
				Devices: domainschema.Devices{
					Interfaces: []domainschema.Interface{*existingIface}}}

			mutatedDomSpec, err := testMutator.Mutate(testDomSpec)
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomSpec.Devices.Interfaces).To(Equal([]domainschema.Interface{*existingIface, *expectedDomainIface}))
		})

		It("should set domain interface correctly when executed more than once", func() {
			networks := []vmschema.Network{*vmschema.DefaultPodNetwork()}
			ifaces := []vmschema.Interface{{Name: "default", Binding: &vmschema.PluginBinding{Name: "vhostuser"}}}

			expectedDomainIface := &domainschema.Interface{
				Alias:  domainschema.NewUserDefinedAlias("default"),
				Type:   "vhostuser",
				Source: domainschema.InterfaceSource{Type: "unix", Path: "/var/run/kubevirt-private/vmi-disks/vhost-user/test/default.sock", Mode: "server"},
				Model:  &domainschema.Model{Type: "virtio"},
			}

			testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, testPodId)
			Expect(err).ToNot(HaveOccurred())

			testDomSpec := &domainschema.DomainSpec{}

			mutatedDomSpec, err := testMutator.Mutate(testDomSpec)
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomSpec.Devices.Interfaces).To(Equal([]domainschema.Interface{*expectedDomainIface}))

			Expect(testMutator.Mutate(mutatedDomSpec)).To(Equal(mutatedDomSpec))
		})

		It("should set memory backing to shared", func() {
			networks := []vmschema.Network{*vmschema.DefaultPodNetwork()}
			ifaces := []vmschema.Interface{{Name: "default", Binding: &vmschema.PluginBinding{Name: "vhostuser"}}}

			testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, testPodId)
			Expect(err).ToNot(HaveOccurred())

			testDomSpec := &domainschema.DomainSpec{}

			mutatedDomSpec, err := testMutator.Mutate(testDomSpec)
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomSpec.MemoryBacking).ToNot(BeNil())
			Expect(mutatedDomSpec.MemoryBacking.Access).ToNot(BeNil())
			Expect(mutatedDomSpec.MemoryBacking.Access.Mode).To(Equal("shared"))
		})

		It("should set memory backing to shared even if it exists with different mode", func() {
			networks := []vmschema.Network{*vmschema.DefaultPodNetwork()}
			ifaces := []vmschema.Interface{{Name: "default", Binding: &vmschema.PluginBinding{Name: "vhostuser"}}}

			testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, testPodId)
			Expect(err).ToNot(HaveOccurred())

			testDomSpec := &domainschema.DomainSpec{
				MemoryBacking: &domainschema.MemoryBacking{
					Access: &domainschema.MemoryBackingAccess{
						Mode: "private",
					},
				},
			}

			mutatedDomSpec, err := testMutator.Mutate(testDomSpec)
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomSpec.MemoryBacking).ToNot(BeNil())
			Expect(mutatedDomSpec.MemoryBacking.Access).ToNot(BeNil())
			Expect(mutatedDomSpec.MemoryBacking.Access.Mode).To(Equal("shared"))
		})

		It("should handle multiple vhostuser interfaces correctly", func() {
			networks := []vmschema.Network{
				*vmschema.DefaultPodNetwork(),
				{Name: "network1", NetworkSource: vmschema.NetworkSource{Multus: &vmschema.MultusNetwork{NetworkName: "net1"}}},
				{Name: "network2", NetworkSource: vmschema.NetworkSource{Multus: &vmschema.MultusNetwork{NetworkName: "net2"}}},
			}
			ifaces := []vmschema.Interface{
				{Name: "default", Binding: &vmschema.PluginBinding{Name: "vhostuser"}},
				{Name: "net1", Binding: &vmschema.PluginBinding{Name: "vhostuser"}, MacAddress: "02:00:00:00:00:01"},
				{Name: "net2", Binding: &vmschema.PluginBinding{Name: "vhostuser"}, MacAddress: "02:00:00:00:00:02", PciAddress: "0000:03:00.0"},
			}

			expectedDomainIfaces := []domainschema.Interface{
				{
					Alias:  domainschema.NewUserDefinedAlias("default"),
					Type:   "vhostuser",
					Source: domainschema.InterfaceSource{Type: "unix", Path: "/var/run/kubevirt-private/vmi-disks/vhost-user/test/default.sock", Mode: "server"},
					Model:  &domainschema.Model{Type: "virtio"},
				},
				{
					Alias:  domainschema.NewUserDefinedAlias("net1"),
					Type:   "vhostuser",
					Source: domainschema.InterfaceSource{Type: "unix", Path: "/var/run/kubevirt-private/vmi-disks/vhost-user/test/net1.sock", Mode: "server"},
					Model:  &domainschema.Model{Type: "virtio"},
					MAC:    &domainschema.MAC{MAC: "02:00:00:00:00:01"},
				},
				{
					Alias:   domainschema.NewUserDefinedAlias("net2"),
					Type:    "vhostuser",
					Source:  domainschema.InterfaceSource{Type: "unix", Path: "/var/run/kubevirt-private/vmi-disks/vhost-user/test/net2.sock", Mode: "server"},
					Model:   &domainschema.Model{Type: "virtio"},
					MAC:     &domainschema.MAC{MAC: "02:00:00:00:00:02"},
					Address: &domainschema.Address{Type: "pci", Domain: "0x0000", Bus: "0x03", Slot: "0x00", Function: "0x0"},
				},
			}

			testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, testPodId)
			Expect(err).ToNot(HaveOccurred())

			mutatedDomSpec, err := testMutator.Mutate(&domainschema.DomainSpec{})
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomSpec.Devices.Interfaces).To(HaveLen(3))
			Expect(mutatedDomSpec.Devices.Interfaces).To(Equal(expectedDomainIfaces))
			Expect(mutatedDomSpec.MemoryBacking).ToNot(BeNil())
			Expect(mutatedDomSpec.MemoryBacking.Access).ToNot(BeNil())
			Expect(mutatedDomSpec.MemoryBacking.Access.Mode).To(Equal("shared"))
		})

		It("should replace existing interface with same name", func() {
			networks := []vmschema.Network{*vmschema.DefaultPodNetwork()}
			ifaces := []vmschema.Interface{{Name: "default", Binding: &vmschema.PluginBinding{Name: "vhostuser"}}}

			testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, testPodId)
			Expect(err).ToNot(HaveOccurred())

			existingIface := &domainschema.Interface{
				Alias: domainschema.NewUserDefinedAlias("default"),
				Type:  "bridge",
			}
			testDomSpec := &domainschema.DomainSpec{
				Devices: domainschema.Devices{
					Interfaces: []domainschema.Interface{*existingIface},
				},
			}

			expectedDomainIface := &domainschema.Interface{
				Alias:  domainschema.NewUserDefinedAlias("default"),
				Type:   "vhostuser",
				Source: domainschema.InterfaceSource{Type: "unix", Path: "/var/run/kubevirt-private/vmi-disks/vhost-user/test/default.sock", Mode: "server"},
				Model:  &domainschema.Model{Type: "virtio"},
			}

			mutatedDomSpec, err := testMutator.Mutate(testDomSpec)
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomSpec.Devices.Interfaces).To(HaveLen(1))
			Expect(mutatedDomSpec.Devices.Interfaces[0]).To(Equal(*expectedDomainIface))
		})

		It("should set memory backing access when backing exists but access is nil", func() {
			networks := []vmschema.Network{*vmschema.DefaultPodNetwork()}
			ifaces := []vmschema.Interface{{Name: "default", Binding: &vmschema.PluginBinding{Name: "vhostuser"}}}

			testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, testPodId)
			Expect(err).ToNot(HaveOccurred())

			testDomSpec := &domainschema.DomainSpec{
				MemoryBacking: &domainschema.MemoryBacking{},
			}

			mutatedDomSpec, err := testMutator.Mutate(testDomSpec)
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomSpec.MemoryBacking).ToNot(BeNil())
			Expect(mutatedDomSpec.MemoryBacking.Access).ToNot(BeNil())
			Expect(mutatedDomSpec.MemoryBacking.Access.Mode).To(Equal("shared"))
		})

		It("should handle mixed vhostuser and non-vhostuser interfaces", func() {
			networks := []vmschema.Network{
				*vmschema.DefaultPodNetwork(),
				{Name: "multus1", NetworkSource: vmschema.NetworkSource{Multus: &vmschema.MultusNetwork{NetworkName: "net1"}}},
				{Name: "multus2", NetworkSource: vmschema.NetworkSource{Multus: &vmschema.MultusNetwork{NetworkName: "net2"}}},
			}
			ifaces := []vmschema.Interface{
				{Name: "default", Binding: &vmschema.PluginBinding{Name: "vhostuser"}},
				{Name: "multus1", InterfaceBindingMethod: vmschema.InterfaceBindingMethod{Bridge: &vmschema.InterfaceBridge{}}},
				{Name: "multus2", Binding: &vmschema.PluginBinding{Name: "vhostuser"}},
			}

			testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, testPodId)
			Expect(err).ToNot(HaveOccurred())

			existingBridgeIface := &domainschema.Interface{
				Alias: domainschema.NewUserDefinedAlias("multus1"),
				Type:  "bridge",
			}
			testDomSpec := &domainschema.DomainSpec{
				Devices: domainschema.Devices{
					Interfaces: []domainschema.Interface{*existingBridgeIface},
				},
			}

			mutatedDomSpec, err := testMutator.Mutate(testDomSpec)
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomSpec.Devices.Interfaces).To(HaveLen(3))

			// Bridge interface should remain untouched
			Expect(mutatedDomSpec.Devices.Interfaces[0].Type).To(Equal("bridge"))
			Expect(mutatedDomSpec.Devices.Interfaces[0].Alias.GetName()).To(Equal("multus1"))

			// Vhostuser interfaces should be added
			vhostuserIfaces := 0
			for _, iface := range mutatedDomSpec.Devices.Interfaces {
				if iface.Type == "vhostuser" {
					vhostuserIfaces++
				}
			}
			Expect(vhostuserIfaces).To(Equal(2))
		})
	})
})
