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
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"

	networkv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	vmschema "kubevirt.io/api/core/v1"

	"kubevirt.io/network-vhostuser-binding/domain"
	"kubevirt.io/kubevirt/pkg/network/downwardapi"

	domainschema "kubevirt.io/kubevirt/pkg/virt-launcher/virtwrap/api"
)

// randID returns a random 5-character alphanumeric string, simulating the
// deployment-specific directory names that device plugins inject into socket paths.
func randID() string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 5)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

// inputSocketPath returns a device-plugin-style socket path with a random
// directory component, e.g. "/var/run/vhost-user/a3xZ9/vhost.sock".
// The random segment simulates paths that change across pod deployments.
func inputSocketPath(_ string) string {
	return fmt.Sprintf("/var/run/vhost-user/%s/vhost.sock", randID())
}

// expectedSocketPath returns the stable socket path that should appear in the
// domain XML after symlinking, e.g. "<socketDir>/<ifaceName>/vhost.sock".
func expectedSocketPath(socketDir, ifaceName string) string {
	return filepath.Join(socketDir, ifaceName, "vhost.sock")
}

func multusNetwork(name string) vmschema.Network {
	return vmschema.Network{
		Name:          name,
		NetworkSource: vmschema.NetworkSource{Multus: &vmschema.MultusNetwork{NetworkName: name}},
	}
}

// writeNetInfoFile writes a downward API network-info file with valid vhost-user entries
// for each named secondary network. The default pod network is not included, matching
// production behavior (only interfaces with DeviceInfo appear in the file).
func writeNetInfoFile(secondaryIfaceNames ...string) string {
	var ifaces []downwardapi.Interface
	for _, name := range secondaryIfaceNames {
		ifaces = append(ifaces, downwardapi.Interface{
			Network: name,
			DeviceInfo: &networkv1.DeviceInfo{
				Type:    networkv1.DeviceInfoTypeVHostUser,
				Version: networkv1.DeviceInfoVersion,
				VhostUser: &networkv1.VhostDevice{
					Mode: networkv1.VhostDeviceModeClient,
					Path: inputSocketPath(name),
				},
			},
		})
	}
	return writeNetInfoFileRaw(ifaces...)
}

// writeNetInfoFileRaw writes a network-info JSON file from the given interfaces and
// returns the file path.
func writeNetInfoFileRaw(ifaces ...downwardapi.Interface) string {
	data, err := json.Marshal(downwardapi.NetworkInfo{Interfaces: ifaces})
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	f := filepath.Join(GinkgoT().TempDir(), "network-info")
	ExpectWithOffset(1, os.WriteFile(f, data, 0644)).To(Succeed())
	return f
}

// testOpts builds VhostUserConfiguratorOptions with the netinfo override pointed at a
// temp file containing valid vhost-user entries for the given interface names.
// It returns the opts and the socket directory used, so callers can build expected paths.
func testOpts(queues uint, ifaceNames ...string) (domain.VhostUserConfiguratorOptions, string) {
	socketDir := filepath.Join(GinkgoT().TempDir(), "vhost-binding")
	opts := domain.VhostUserConfiguratorOptions{Queues: queues}
	opts.SetNetInfoOverride(writeNetInfoFile(ifaceNames...))
	opts.SetSocketDirOverride(socketDir)
	return opts, socketDir
}

// Helper function to create expected interface with common defaults
func newExpectedInterface(socketDir, name string, address *domainschema.Address, mac *domainschema.MAC, acpi *domainschema.ACPI, queues uint) *domainschema.Interface {
	return newExpectedInterfaceWithModel(socketDir, name, address, mac, acpi, queues, "virtio")
}

// Helper function to create expected interface with custom model type
func newExpectedInterfaceWithModel(socketDir, name string, address *domainschema.Address, mac *domainschema.MAC, acpi *domainschema.ACPI, queues uint, modelType string) *domainschema.Interface {
	queueSize := uint(domain.QueueSize)
	return &domainschema.Interface{
		Alias:   domainschema.NewUserDefinedAlias(name),
		Type:    "vhostuser",
		Source:  domainschema.InterfaceSource{Type: "unix", Path: expectedSocketPath(socketDir, name), Mode: "server"},
		Model:   &domainschema.Model{Type: modelType},
		Address: address,
		MAC:     mac,
		ACPI:    acpi,
		Driver:  &domainschema.InterfaceDriver{TXQueueSize: &queueSize, RXQueueSize: &queueSize, Queues: &queues},
	}
}

var _ = Describe("vhostuser network configurator", func() {
	Context("generate domain spec interface", func() {
		DescribeTable("should fail to create configurator given",
			func(ifaces []vmschema.Interface, networks []vmschema.Network) {
				opts, _ := testOpts(1, "net1")
				_, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, opts)

				Expect(err).To(HaveOccurred())
			},
			Entry("no interfaces",
				[]vmschema.Interface{*vmschema.DefaultMasqueradeNetworkInterface()},
				[]vmschema.Network{*vmschema.DefaultPodNetwork(), multusNetwork("net1")},
			),
			Entry("interface with no vhostuser binding method",
				[]vmschema.Interface{*vmschema.DefaultMasqueradeNetworkInterface(), {Name: "net1", InterfaceBindingMethod: vmschema.InterfaceBindingMethod{Bridge: &vmschema.InterfaceBridge{}}}},
				[]vmschema.Network{*vmschema.DefaultPodNetwork(), multusNetwork("net1")},
			),
			Entry("interface with no vhostuser binding plugin",
				[]vmschema.Interface{*vmschema.DefaultMasqueradeNetworkInterface(), {Name: "net1", Binding: &vmschema.PluginBinding{Name: "no-vhostuser"}}},
				[]vmschema.Network{*vmschema.DefaultPodNetwork(), multusNetwork("net1")},
			),
		)

		It("should fail given interface with invalid PCI address", func() {
			ifaces := []vmschema.Interface{
				*vmschema.DefaultMasqueradeNetworkInterface(),
				{Name: "net1", Binding: &vmschema.PluginBinding{Name: "vhostuser"}, PciAddress: "invalid-pci-address"},
			}
			networks := []vmschema.Network{*vmschema.DefaultPodNetwork(), multusNetwork("net1")}

			opts, _ := testOpts(1, "net1")
			testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, opts)
			Expect(err).ToNot(HaveOccurred())

			_, err = testMutator.Mutate(&domainschema.DomainSpec{})
			Expect(err).To(HaveOccurred())
		})

		DescribeTable("should add interface to domain spec given iface with",
			func(iface *vmschema.Interface, buildExpected func(socketDir string) *domainschema.Interface) {
				ifaces := []vmschema.Interface{*vmschema.DefaultMasqueradeNetworkInterface(), *iface}
				networks := []vmschema.Network{*vmschema.DefaultPodNetwork(), multusNetwork(iface.Name)}

				opts, socketDir := testOpts(1, iface.Name)
				testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, opts)
				Expect(err).ToNot(HaveOccurred())

				mutatedDomSpec, err := testMutator.Mutate(&domainschema.DomainSpec{})
				Expect(err).ToNot(HaveOccurred())
				Expect(mutatedDomSpec.Devices.Interfaces).To(Equal([]domainschema.Interface{*buildExpected(socketDir)}))
			},
			Entry("vhostuser binding plugin",
				&vmschema.Interface{Name: "net1", Binding: &vmschema.PluginBinding{Name: "vhostuser"}},
				func(socketDir string) *domainschema.Interface {
					return newExpectedInterface(socketDir, "net1", nil, nil, nil, 1)
				},
			),
			Entry("PCI address",
				&vmschema.Interface{Name: "net1", Binding: &vmschema.PluginBinding{Name: "vhostuser"},
					PciAddress: "0000:02:02.0"},
				func(socketDir string) *domainschema.Interface {
					return newExpectedInterface(socketDir, "net1",
						&domainschema.Address{Type: "pci", Domain: "0x0000", Bus: "0x02", Slot: "0x02", Function: "0x0"},
						nil, nil, 1)
				},
			),
			Entry("MAC address",
				&vmschema.Interface{Name: "net1", Binding: &vmschema.PluginBinding{Name: "vhostuser"},
					MacAddress: "02:02:02:02:02:02"},
				func(socketDir string) *domainschema.Interface {
					return newExpectedInterface(socketDir, "net1", nil,
						&domainschema.MAC{MAC: "02:02:02:02:02:02"}, nil, 1)
				},
			),
			Entry("ACPI address",
				&vmschema.Interface{Name: "net1", Binding: &vmschema.PluginBinding{Name: "vhostuser"},
					ACPIIndex: 2},
				func(socketDir string) *domainschema.Interface {
					return newExpectedInterface(socketDir, "net1", nil, nil,
						&domainschema.ACPI{Index: uint(2)}, 1)
				},
			),
		)

		It("should not override other interfaces", func() {
			networks := []vmschema.Network{
				*vmschema.DefaultPodNetwork(),
				multusNetwork("net1"),
				multusNetwork("net2"),
			}
			ifaces := []vmschema.Interface{
				*vmschema.DefaultMasqueradeNetworkInterface(),
				{Name: "net1", Binding: &vmschema.PluginBinding{Name: "vhostuser"}},
				{Name: "net2", InterfaceBindingMethod: vmschema.InterfaceBindingMethod{Bridge: &vmschema.InterfaceBridge{}}},
			}

			opts, socketDir := testOpts(1, "net1")
			expectedDomainIface := newExpectedInterface(socketDir, "net1", nil, nil, nil, 1)

			testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, opts)
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
			networks := []vmschema.Network{*vmschema.DefaultPodNetwork(), multusNetwork("net1")}
			ifaces := []vmschema.Interface{*vmschema.DefaultMasqueradeNetworkInterface(), {Name: "net1", Binding: &vmschema.PluginBinding{Name: "vhostuser"}}}

			opts, socketDir := testOpts(1, "net1")
			expectedDomainIface := newExpectedInterface(socketDir, "net1", nil, nil, nil, 1)

			testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, opts)
			Expect(err).ToNot(HaveOccurred())

			testDomSpec := &domainschema.DomainSpec{}

			mutatedDomSpec, err := testMutator.Mutate(testDomSpec)
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomSpec.Devices.Interfaces).To(Equal([]domainschema.Interface{*expectedDomainIface}))

			Expect(testMutator.Mutate(mutatedDomSpec)).To(Equal(mutatedDomSpec))
		})

		It("should set memory backing to shared", func() {
			networks := []vmschema.Network{*vmschema.DefaultPodNetwork(), multusNetwork("net1")}
			ifaces := []vmschema.Interface{*vmschema.DefaultMasqueradeNetworkInterface(), {Name: "net1", Binding: &vmschema.PluginBinding{Name: "vhostuser"}}}

			opts, _ := testOpts(1, "net1")
			testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, opts)
			Expect(err).ToNot(HaveOccurred())

			testDomSpec := &domainschema.DomainSpec{}

			mutatedDomSpec, err := testMutator.Mutate(testDomSpec)
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomSpec.MemoryBacking).ToNot(BeNil())
			Expect(mutatedDomSpec.MemoryBacking.Access).ToNot(BeNil())
			Expect(mutatedDomSpec.MemoryBacking.Access.Mode).To(Equal("shared"))
		})

		It("should set memory backing to shared even if it exists with different mode", func() {
			networks := []vmschema.Network{*vmschema.DefaultPodNetwork(), multusNetwork("net1")}
			ifaces := []vmschema.Interface{*vmschema.DefaultMasqueradeNetworkInterface(), {Name: "net1", Binding: &vmschema.PluginBinding{Name: "vhostuser"}}}

			opts, _ := testOpts(1, "net1")
			testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, opts)
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
				multusNetwork("net1"),
				multusNetwork("net2"),
				multusNetwork("net3"),
			}
			ifaces := []vmschema.Interface{
				*vmschema.DefaultMasqueradeNetworkInterface(),
				{Name: "net1", Binding: &vmschema.PluginBinding{Name: "vhostuser"}},
				{Name: "net2", Binding: &vmschema.PluginBinding{Name: "vhostuser"}, MacAddress: "02:00:00:00:00:01"},
				{Name: "net3", Binding: &vmschema.PluginBinding{Name: "vhostuser"}, MacAddress: "02:00:00:00:00:02", PciAddress: "0000:03:00.0"},
			}

			opts, socketDir := testOpts(1, "net1", "net2", "net3")
			expectedDomainIfaces := []domainschema.Interface{
				*newExpectedInterface(socketDir, "net1", nil, nil, nil, 1),
				*newExpectedInterface(socketDir, "net2", nil, &domainschema.MAC{MAC: "02:00:00:00:00:01"}, nil, 1),
				*newExpectedInterface(socketDir, "net3",
					&domainschema.Address{Type: "pci", Domain: "0x0000", Bus: "0x03", Slot: "0x00", Function: "0x0"},
					&domainschema.MAC{MAC: "02:00:00:00:00:02"}, nil, 1),
			}

			testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, opts)
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
			networks := []vmschema.Network{*vmschema.DefaultPodNetwork(), multusNetwork("net1")}
			ifaces := []vmschema.Interface{*vmschema.DefaultMasqueradeNetworkInterface(), {Name: "net1", Binding: &vmschema.PluginBinding{Name: "vhostuser"}}}

			opts, socketDir := testOpts(1, "net1")
			testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, opts)
			Expect(err).ToNot(HaveOccurred())

			existingIface := &domainschema.Interface{
				Alias: domainschema.NewUserDefinedAlias("net1"),
				Type:  "bridge",
			}
			testDomSpec := &domainschema.DomainSpec{
				Devices: domainschema.Devices{
					Interfaces: []domainschema.Interface{*existingIface},
				},
			}

			expectedDomainIface := newExpectedInterface(socketDir, "net1", nil, nil, nil, 1)

			mutatedDomSpec, err := testMutator.Mutate(testDomSpec)
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomSpec.Devices.Interfaces).To(HaveLen(1))
			Expect(mutatedDomSpec.Devices.Interfaces[0]).To(Equal(*expectedDomainIface))
		})

		It("should set memory backing access when backing exists but access is nil", func() {
			networks := []vmschema.Network{*vmschema.DefaultPodNetwork(), multusNetwork("net1")}
			ifaces := []vmschema.Interface{*vmschema.DefaultMasqueradeNetworkInterface(), {Name: "net1", Binding: &vmschema.PluginBinding{Name: "vhostuser"}}}

			opts, _ := testOpts(1, "net1")
			testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, opts)
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
				multusNetwork("net1"),
				multusNetwork("net2"),
				multusNetwork("net3"),
			}
			ifaces := []vmschema.Interface{
				*vmschema.DefaultMasqueradeNetworkInterface(),
				{Name: "net1", Binding: &vmschema.PluginBinding{Name: "vhostuser"}},
				{Name: "net2", InterfaceBindingMethod: vmschema.InterfaceBindingMethod{Bridge: &vmschema.InterfaceBridge{}}},
				{Name: "net3", Binding: &vmschema.PluginBinding{Name: "vhostuser"}},
			}

			opts, _ := testOpts(1, "net1", "net3")
			testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, opts)
			Expect(err).ToNot(HaveOccurred())

			existingBridgeIface := &domainschema.Interface{
				Alias: domainschema.NewUserDefinedAlias("net2"),
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
			Expect(mutatedDomSpec.Devices.Interfaces[0].Alias.GetName()).To(Equal("net2"))

			// Vhostuser interfaces should be added
			vhostuserIfaces := 0
			for _, iface := range mutatedDomSpec.Devices.Interfaces {
				if iface.Type == "vhostuser" {
					vhostuserIfaces++
				}
			}
			Expect(vhostuserIfaces).To(Equal(2))
		})

		It("should set queues to 1 when multiqueue is disabled", func() {
			networks := []vmschema.Network{*vmschema.DefaultPodNetwork(), multusNetwork("net1")}
			ifaces := []vmschema.Interface{*vmschema.DefaultMasqueradeNetworkInterface(), {Name: "net1", Binding: &vmschema.PluginBinding{Name: "vhostuser"}}}

			opts, _ := testOpts(1, "net1")
			testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, opts)
			Expect(err).ToNot(HaveOccurred())

			testDomSpec := &domainschema.DomainSpec{
				VCPU: &domainschema.VCPU{CPUs: 4},
			}

			mutatedDomSpec, err := testMutator.Mutate(testDomSpec)
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomSpec.Devices.Interfaces).To(HaveLen(1))

			iface := mutatedDomSpec.Devices.Interfaces[0]
			Expect(iface.Driver).ToNot(BeNil())
			Expect(iface.Driver.Queues).ToNot(BeNil())
			Expect(*iface.Driver.Queues).To(Equal(uint(1)))
		})

		It("should set queues to number of vCPUs when multiqueue is enabled", func() {
			networks := []vmschema.Network{*vmschema.DefaultPodNetwork(), multusNetwork("net1")}
			ifaces := []vmschema.Interface{*vmschema.DefaultMasqueradeNetworkInterface(), {Name: "net1", Binding: &vmschema.PluginBinding{Name: "vhostuser"}}}

			opts, _ := testOpts(4, "net1")
			testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, opts)
			Expect(err).ToNot(HaveOccurred())

			testDomSpec := &domainschema.DomainSpec{
				VCPU: &domainschema.VCPU{CPUs: 4},
			}

			mutatedDomSpec, err := testMutator.Mutate(testDomSpec)
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomSpec.Devices.Interfaces).To(HaveLen(1))

			iface := mutatedDomSpec.Devices.Interfaces[0]
			Expect(iface.Driver).ToNot(BeNil())
			Expect(iface.Driver.Queues).ToNot(BeNil())
			Expect(*iface.Driver.Queues).To(Equal(uint(4)))
		})

		It("should set queues to 1 when multiqueue is enabled but VCPU is nil", func() {
			networks := []vmschema.Network{*vmschema.DefaultPodNetwork(), multusNetwork("net1")}
			ifaces := []vmschema.Interface{*vmschema.DefaultMasqueradeNetworkInterface(), {Name: "net1", Binding: &vmschema.PluginBinding{Name: "vhostuser"}}}

			opts, _ := testOpts(1, "net1")
			testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, opts)
			Expect(err).ToNot(HaveOccurred())

			testDomSpec := &domainschema.DomainSpec{}

			mutatedDomSpec, err := testMutator.Mutate(testDomSpec)
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomSpec.Devices.Interfaces).To(HaveLen(1))

			iface := mutatedDomSpec.Devices.Interfaces[0]
			Expect(iface.Driver).ToNot(BeNil())
			Expect(iface.Driver.Queues).ToNot(BeNil())
			Expect(*iface.Driver.Queues).To(Equal(uint(1)))
		})

		It("should set queue sizes to 1024", func() {
			networks := []vmschema.Network{*vmschema.DefaultPodNetwork(), multusNetwork("net1")}
			ifaces := []vmschema.Interface{*vmschema.DefaultMasqueradeNetworkInterface(), {Name: "net1", Binding: &vmschema.PluginBinding{Name: "vhostuser"}}}

			opts, _ := testOpts(1, "net1")
			testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, opts)
			Expect(err).ToNot(HaveOccurred())

			testDomSpec := &domainschema.DomainSpec{}

			mutatedDomSpec, err := testMutator.Mutate(testDomSpec)
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomSpec.Devices.Interfaces).To(HaveLen(1))

			iface := mutatedDomSpec.Devices.Interfaces[0]
			Expect(iface.Driver).ToNot(BeNil())
			Expect(iface.Driver.TXQueueSize).ToNot(BeNil())
			Expect(*iface.Driver.TXQueueSize).To(Equal(uint(1024)))
			Expect(iface.Driver.RXQueueSize).ToNot(BeNil())
			Expect(*iface.Driver.RXQueueSize).To(Equal(uint(1024)))
		})

		It("should handle multiqueue with multiple interfaces", func() {
			networks := []vmschema.Network{
				*vmschema.DefaultPodNetwork(),
				multusNetwork("net1"),
				multusNetwork("net2"),
			}
			ifaces := []vmschema.Interface{
				*vmschema.DefaultMasqueradeNetworkInterface(),
				{Name: "net1", Binding: &vmschema.PluginBinding{Name: "vhostuser"}},
				{Name: "net2", Binding: &vmschema.PluginBinding{Name: "vhostuser"}},
			}

			opts, _ := testOpts(8, "net1", "net2")
			testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, opts)
			Expect(err).ToNot(HaveOccurred())

			testDomSpec := &domainschema.DomainSpec{
				VCPU: &domainschema.VCPU{CPUs: 8},
			}

			mutatedDomSpec, err := testMutator.Mutate(testDomSpec)
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomSpec.Devices.Interfaces).To(HaveLen(2))

			for _, iface := range mutatedDomSpec.Devices.Interfaces {
				Expect(iface.Driver).ToNot(BeNil())
				Expect(iface.Driver.Queues).ToNot(BeNil())
				Expect(*iface.Driver.Queues).To(Equal(uint(8)))
			}
		})

		It("should set model to virtio when UseVirtioTransitional is false", func() {
			networks := []vmschema.Network{*vmschema.DefaultPodNetwork(), multusNetwork("net1")}
			ifaces := []vmschema.Interface{*vmschema.DefaultMasqueradeNetworkInterface(), {Name: "net1", Binding: &vmschema.PluginBinding{Name: "vhostuser"}}}

			opts, _ := testOpts(1, "net1")
			opts.UseVirtioTransitional = false
			testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, opts)
			Expect(err).ToNot(HaveOccurred())

			mutatedDomSpec, err := testMutator.Mutate(&domainschema.DomainSpec{})
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomSpec.Devices.Interfaces).To(HaveLen(1))
			Expect(mutatedDomSpec.Devices.Interfaces[0].Model).ToNot(BeNil())
			Expect(mutatedDomSpec.Devices.Interfaces[0].Model.Type).To(Equal("virtio"))
		})

		It("should set model to virtio-transitional when UseVirtioTransitional is true", func() {
			networks := []vmschema.Network{*vmschema.DefaultPodNetwork(), multusNetwork("net1")}
			ifaces := []vmschema.Interface{*vmschema.DefaultMasqueradeNetworkInterface(), {Name: "net1", Binding: &vmschema.PluginBinding{Name: "vhostuser"}}}

			opts, socketDir := testOpts(1, "net1")
			expectedDomainIface := newExpectedInterfaceWithModel(socketDir, "net1", nil, nil, nil, 1, "virtio-transitional")

			opts.UseVirtioTransitional = true
			testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, opts)
			Expect(err).ToNot(HaveOccurred())

			mutatedDomSpec, err := testMutator.Mutate(&domainschema.DomainSpec{})
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomSpec.Devices.Interfaces).To(Equal([]domainschema.Interface{*expectedDomainIface}))
		})

		It("should apply UseVirtioTransitional to all vhostuser interfaces", func() {
			networks := []vmschema.Network{
				*vmschema.DefaultPodNetwork(),
				multusNetwork("net1"),
				multusNetwork("net2"),
			}
			ifaces := []vmschema.Interface{
				*vmschema.DefaultMasqueradeNetworkInterface(),
				{Name: "net1", Binding: &vmschema.PluginBinding{Name: "vhostuser"}},
				{Name: "net2", Binding: &vmschema.PluginBinding{Name: "vhostuser"}},
			}

			opts, _ := testOpts(1, "net1", "net2")
			opts.UseVirtioTransitional = true
			testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, opts)
			Expect(err).ToNot(HaveOccurred())

			mutatedDomSpec, err := testMutator.Mutate(&domainschema.DomainSpec{})
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomSpec.Devices.Interfaces).To(HaveLen(2))

			for _, iface := range mutatedDomSpec.Devices.Interfaces {
				Expect(iface.Model).ToNot(BeNil())
				Expect(iface.Model.Type).To(Equal("virtio-transitional"))
			}
		})

		It("should fail when deviceInfo has non-vhostuser type for a vhostuser interface", func() {
			networks := []vmschema.Network{*vmschema.DefaultPodNetwork(), multusNetwork("net1")}
			ifaces := []vmschema.Interface{*vmschema.DefaultMasqueradeNetworkInterface(), {Name: "net1", Binding: &vmschema.PluginBinding{Name: "vhostuser"}}}

			netInfoPath := writeNetInfoFileRaw(downwardapi.Interface{
				Network: "net1",
				DeviceInfo: &networkv1.DeviceInfo{
					Type: networkv1.DeviceInfoTypePCI,
					Pci:  &networkv1.PciDevice{PciAddress: "0000:01:00.0"},
				},
			})

			opts := domain.VhostUserConfiguratorOptions{Queues: 1}
			opts.SetNetInfoOverride(netInfoPath)
			opts.SetSocketDirOverride(filepath.Join(GinkgoT().TempDir(), "vhost-binding"))
			testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, opts)
			Expect(err).ToNot(HaveOccurred())

			_, err = testMutator.Mutate(&domainschema.DomainSpec{})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found"))
		})

		It("should fail when deviceInfo has wrong vhost-user mode", func() {
			networks := []vmschema.Network{*vmschema.DefaultPodNetwork(), multusNetwork("net1")}
			ifaces := []vmschema.Interface{*vmschema.DefaultMasqueradeNetworkInterface(), {Name: "net1", Binding: &vmschema.PluginBinding{Name: "vhostuser"}}}

			netInfoPath := writeNetInfoFileRaw(downwardapi.Interface{
				Network: "net1",
				DeviceInfo: &networkv1.DeviceInfo{
					Type: networkv1.DeviceInfoTypeVHostUser,
					VhostUser: &networkv1.VhostDevice{
						Mode: networkv1.VhostDeviceModeServer,
						Path: "/some/path.sock",
					},
				},
			})

			opts := domain.VhostUserConfiguratorOptions{Queues: 1}
			opts.SetNetInfoOverride(netInfoPath)
			opts.SetSocketDirOverride(filepath.Join(GinkgoT().TempDir(), "vhost-binding"))
			_, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, opts)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("wrong vhost-user mode"))
		})

		It("should fail when deviceInfo is missing for interface", func() {
			networks := []vmschema.Network{*vmschema.DefaultPodNetwork(), multusNetwork("net1")}
			ifaces := []vmschema.Interface{*vmschema.DefaultMasqueradeNetworkInterface(), {Name: "net1", Binding: &vmschema.PluginBinding{Name: "vhostuser"}}}

			// Write a net-info file with no interfaces
			netInfoPath := writeNetInfoFileRaw()

			opts := domain.VhostUserConfiguratorOptions{Queues: 1}
			opts.SetNetInfoOverride(netInfoPath)
			opts.SetSocketDirOverride(filepath.Join(GinkgoT().TempDir(), "vhost-binding"))
			testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, opts)
			Expect(err).ToNot(HaveOccurred())

			_, err = testMutator.Mutate(&domainschema.DomainSpec{})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found"))
		})

		It("should set MTU on domain interface when network-info provides it", func() {
			networks := []vmschema.Network{*vmschema.DefaultPodNetwork(), multusNetwork("net1")}
			ifaces := []vmschema.Interface{*vmschema.DefaultMasqueradeNetworkInterface(), {Name: "net1", Binding: &vmschema.PluginBinding{Name: "vhostuser"}}}

			socketDir := filepath.Join(GinkgoT().TempDir(), "vhost-binding")
			netInfoPath := writeNetInfoFileRaw(downwardapi.Interface{
				Network: "net1",
				Mtu:     9000,
				DeviceInfo: &networkv1.DeviceInfo{
					Type:    networkv1.DeviceInfoTypeVHostUser,
					Version: networkv1.DeviceInfoVersion,
					VhostUser: &networkv1.VhostDevice{
						Mode: networkv1.VhostDeviceModeClient,
						Path: inputSocketPath("net1"),
					},
				},
			})

			opts := domain.VhostUserConfiguratorOptions{Queues: 1}
			opts.SetNetInfoOverride(netInfoPath)
			opts.SetSocketDirOverride(socketDir)

			testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, opts)
			Expect(err).ToNot(HaveOccurred())

			mutatedDomSpec, err := testMutator.Mutate(&domainschema.DomainSpec{})
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomSpec.Devices.Interfaces).To(HaveLen(1))
			Expect(mutatedDomSpec.Devices.Interfaces[0].MTU).ToNot(BeNil())
			Expect(mutatedDomSpec.Devices.Interfaces[0].MTU.Size).To(Equal("9000"))
		})

		It("should not set MTU on domain interface when network-info has no MTU", func() {
			networks := []vmschema.Network{*vmschema.DefaultPodNetwork(), multusNetwork("net1")}
			ifaces := []vmschema.Interface{*vmschema.DefaultMasqueradeNetworkInterface(), {Name: "net1", Binding: &vmschema.PluginBinding{Name: "vhostuser"}}}

			opts, _ := testOpts(1, "net1")
			testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, opts)
			Expect(err).ToNot(HaveOccurred())

			mutatedDomSpec, err := testMutator.Mutate(&domainschema.DomainSpec{})
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomSpec.Devices.Interfaces).To(HaveLen(1))
			Expect(mutatedDomSpec.Devices.Interfaces[0].MTU).To(BeNil())
		})
	})

	Context("socket symlink management", func() {
		It("should create symlink pointing to original socket directory", func() {
			networks := []vmschema.Network{*vmschema.DefaultPodNetwork(), multusNetwork("net1")}
			ifaces := []vmschema.Interface{
				*vmschema.DefaultMasqueradeNetworkInterface(),
				{Name: "net1", Binding: &vmschema.PluginBinding{Name: "vhostuser"}},
			}

			opts, socketDir := testOpts(1, "net1")
			testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, opts)
			Expect(err).ToNot(HaveOccurred())

			mutatedDomSpec, err := testMutator.Mutate(&domainschema.DomainSpec{})
			Expect(err).ToNot(HaveOccurred())

			Expect(mutatedDomSpec.Devices.Interfaces).To(HaveLen(1))

			// The symlink should exist and point into the original socket tree
			symlinkPath := filepath.Join(socketDir, "net1")
			target, err := os.Readlink(symlinkPath)
			Expect(err).ToNot(HaveOccurred())
			Expect(target).To(HavePrefix("/var/run/vhost-user/"))
		})

		It("should replace existing symlink on repeated calls", func() {
			networks := []vmschema.Network{*vmschema.DefaultPodNetwork(), multusNetwork("net1")}
			ifaces := []vmschema.Interface{
				*vmschema.DefaultMasqueradeNetworkInterface(),
				{Name: "net1", Binding: &vmschema.PluginBinding{Name: "vhostuser"}},
			}

			opts, socketDir := testOpts(1, "net1")
			testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, opts)
			Expect(err).ToNot(HaveOccurred())

			_, err = testMutator.Mutate(&domainschema.DomainSpec{})
			Expect(err).ToNot(HaveOccurred())

			// Mutate again — should succeed, replacing the existing symlink
			_, err = testMutator.Mutate(&domainschema.DomainSpec{})
			Expect(err).ToNot(HaveOccurred())

			symlinkPath := filepath.Join(socketDir, "net1")
			target, err := os.Readlink(symlinkPath)
			Expect(err).ToNot(HaveOccurred())
			Expect(target).To(HavePrefix("/var/run/vhost-user/"))
		})

		It("should produce stable paths regardless of original socket directory", func() {
			networks := []vmschema.Network{*vmschema.DefaultPodNetwork(), multusNetwork("net1")}
			ifaces := []vmschema.Interface{
				*vmschema.DefaultMasqueradeNetworkInterface(),
				{Name: "net1", Binding: &vmschema.PluginBinding{Name: "vhostuser"}},
			}

			// Use a device-plugin-style path with a deployment-specific UUID
			socketDir := filepath.Join(GinkgoT().TempDir(), "vhost-binding")
			netInfoPath := writeNetInfoFileRaw(downwardapi.Interface{
				Network: "net1",
				DeviceInfo: &networkv1.DeviceInfo{
					Type:    networkv1.DeviceInfoTypeVHostUser,
					Version: networkv1.DeviceInfoVersion,
					VhostUser: &networkv1.VhostDevice{
						Mode: networkv1.VhostDeviceModeClient,
						Path: "/var/lib/kubelet/plugins/some-dp/deployment-abc123/vhost.sock",
					},
				},
			})

			opts := domain.VhostUserConfiguratorOptions{Queues: 1}
			opts.SetNetInfoOverride(netInfoPath)
			opts.SetSocketDirOverride(socketDir)

			testMutator, err := domain.NewVhostUserNetworkConfigurator(ifaces, networks, opts)
			Expect(err).ToNot(HaveOccurred())

			mutatedDomSpec, err := testMutator.Mutate(&domainschema.DomainSpec{})
			Expect(err).ToNot(HaveOccurred())

			// The domain XML path should use the stable symlink directory, not the original
			Expect(mutatedDomSpec.Devices.Interfaces[0].Source.Path).To(Equal(
				filepath.Join(socketDir, "net1", "vhost.sock"),
			))
			Expect(mutatedDomSpec.Devices.Interfaces[0].Source.Path).ToNot(ContainSubstring("deployment-abc123"))
		})
	})
})
