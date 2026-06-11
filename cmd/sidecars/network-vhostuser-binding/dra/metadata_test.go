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

package dra_test

import (
	"fmt"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"kubevirt.io/network-vhostuser-binding/dra"
)

// metadataJSON returns a minimal valid DeviceMetadata JSON document
// (metadata.resource.k8s.io/v1alpha1) that exposes socketPath as a device
// attribute — matching what a real vhost-user DRA driver would write.
func metadataJSON(socketPath string) string {
	return fmt.Sprintf(
		`{"apiVersion":"metadata.resource.k8s.io/v1alpha1","kind":"DeviceMetadata",`+
			`"metadata":{"name":"test-claim","namespace":"default"},`+
			`"requests":[{"name":"vhost-user","devices":[{"driver":"vhost-user.example.com",`+
			`"pool":"node-0","name":"vhu0","attributes":{%q:{"string":%q}}}]}]}`,
		dra.SocketAttributeKey, socketPath,
	)
}

// writeMetadataFile creates the directory tree expected by [dra.Reader]
// and writes a metadata JSON file into it, returning the root directory.
//
//	<root>/<subDir>/<claimName>/<requestName>/vhost-user.example.com-metadata.json
func writeMetadataFile(subDir, claimName, requestName, socketPath string) string {
	root := GinkgoT().TempDir()
	dir := filepath.Join(root, subDir, claimName, requestName)
	Expect(os.MkdirAll(dir, 0755)).To(Succeed())
	file := filepath.Join(dir, "vhost-user.example.com-metadata.json")
	Expect(os.WriteFile(file, []byte(metadataJSON(socketPath)), 0644)).To(Succeed())
	return root
}

var _ = Describe("Reader", func() {
	const (
		claimName   = "net1"
		requestName = "vhost-user"
		socketPath  = "/var/run/vhost-user/abc12/vhost.sock"
	)

	Context("ReadClaim", func() {
		It("returns SocketMetadata when the metadata file exists", func() {
			root := writeMetadataFile("resourceclaims", claimName, requestName, socketPath)
			reader := dra.Reader{BaseDir: root}

			meta, err := reader.ReadClaim(claimName, requestName)
			Expect(err).ToNot(HaveOccurred())
			Expect(meta.SocketPath).To(Equal(socketPath))
		})

		It("returns an error when no metadata file exists", func() {
			reader := dra.Reader{BaseDir: GinkgoT().TempDir()}

			_, err := reader.ReadClaim(claimName, requestName)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no metadata files found"))
		})

		It("returns an error when the metadata file has no socketPath attribute", func() {
			root := GinkgoT().TempDir()
			dir := filepath.Join(root, "resourceclaims", claimName, requestName)
			Expect(os.MkdirAll(dir, 0755)).To(Succeed())
			noAttrJSON := `{"apiVersion":"metadata.resource.k8s.io/v1alpha1","kind":"DeviceMetadata",` +
				`"metadata":{"name":"test-claim","namespace":"default"},` +
				`"requests":[{"name":"vhost-user","devices":[{"driver":"vhost-user.example.com",` +
				`"pool":"node-0","name":"vhu0","attributes":{}}]}]}`
			Expect(os.WriteFile(
				filepath.Join(dir, "vhost-user.example.com-metadata.json"),
				[]byte(noAttrJSON), 0644,
			)).To(Succeed())

			reader := dra.Reader{BaseDir: root}
			_, err := reader.ReadClaim(claimName, requestName)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(dra.SocketAttributeKey))
		})
	})

	Context("ReadClaimTemplate", func() {
		It("returns SocketMetadata when the metadata file exists", func() {
			root := writeMetadataFile("resourceclaimtemplates", claimName, requestName, socketPath)
			reader := dra.Reader{BaseDir: root}

			meta, err := reader.ReadClaimTemplate(claimName, requestName)
			Expect(err).ToNot(HaveOccurred())
			Expect(meta.SocketPath).To(Equal(socketPath))
		})

		It("returns an error when no metadata file exists", func() {
			reader := dra.Reader{BaseDir: GinkgoT().TempDir()}

			_, err := reader.ReadClaimTemplate(claimName, requestName)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no metadata files found"))
		})
	})
})

var _ = Describe("ResolveForInterface", func() {
	const (
		ifaceName    = "net1"
		podClaimName = "my-claim"
		requestName  = "vhost-port"
		socketPath   = "/var/run/vhost-user/abc12/vhost.sock"
	)

	ref := dra.ResourceRef{PodClaimName: podClaimName, RequestName: requestName}

	It("resolves via ResourceClaim", func() {
		root := writeMetadataFile("resourceclaims", podClaimName, requestName, socketPath)
		reader := dra.Reader{BaseDir: root}

		meta, err := dra.ResolveForInterface(reader, ifaceName, ref)
		Expect(err).ToNot(HaveOccurred())
		Expect(meta.SocketPath).To(Equal(socketPath))
	})

	It("resolves via ResourceClaimTemplate when no ResourceClaim exists", func() {
		root := writeMetadataFile("resourceclaimtemplates", podClaimName, requestName, socketPath)
		reader := dra.Reader{BaseDir: root}

		meta, err := dra.ResolveForInterface(reader, ifaceName, ref)
		Expect(err).ToNot(HaveOccurred())
		Expect(meta.SocketPath).To(Equal(socketPath))
	})

	It("returns an error when neither ResourceClaim nor ResourceClaimTemplate exists", func() {
		reader := dra.Reader{BaseDir: GinkgoT().TempDir()}

		_, err := dra.ResolveForInterface(reader, ifaceName, ref)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no DRA metadata found"))
		Expect(err.Error()).To(ContainSubstring(ifaceName))
	})
})

var _ = Describe("ParseAnnotations", func() {
	It("parses valid annotations into ResourceRefs", func() {
		annotations := map[string]string{
			"resource.vhost-user-binding-plugin.io/foo": "my-claim/vhost1",
			"resource.vhost-user-binding-plugin.io/bar": "my-claim/vhost2",
			"some.other.annotation/foo":                 "ignored",
		}

		refs, err := dra.ParseAnnotations(annotations)
		Expect(err).ToNot(HaveOccurred())
		Expect(refs).To(HaveLen(2))
		Expect(refs["foo"]).To(Equal(dra.ResourceRef{PodClaimName: "my-claim", RequestName: "vhost1"}))
		Expect(refs["bar"]).To(Equal(dra.ResourceRef{PodClaimName: "my-claim", RequestName: "vhost2"}))
	})

	It("returns an empty map when no matching annotations exist", func() {
		refs, err := dra.ParseAnnotations(map[string]string{"unrelated": "value"})
		Expect(err).ToNot(HaveOccurred())
		Expect(refs).To(BeEmpty())
	})

	It("returns an error for a malformed annotation value", func() {
		annotations := map[string]string{
			"resource.vhost-user-binding-plugin.io/foo": "no-slash-here",
		}
		_, err := dra.ParseAnnotations(annotations)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("invalid value"))
	})

	It("returns an error when podClaimName is empty", func() {
		annotations := map[string]string{
			"resource.vhost-user-binding-plugin.io/foo": "/requestName",
		}
		_, err := dra.ParseAnnotations(annotations)
		Expect(err).To(HaveOccurred())
	})

	It("returns an error when requestName is empty", func() {
		annotations := map[string]string{
			"resource.vhost-user-binding-plugin.io/foo": "claimName/",
		}
		_, err := dra.ParseAnnotations(annotations)
		Expect(err).To(HaveOccurred())
	})
})
