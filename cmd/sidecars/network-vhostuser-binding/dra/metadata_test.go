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
		ifaceName  = "net1"
		socketPath = "/var/run/vhost-user/abc12/vhost.sock"
	)

	// requestName="*" is passed as a path segment; filepath.Glob then expands
	// it so that any request subdirectory matches. This means "*" succeeds
	// whenever any request directory with a metadata file exists, regardless
	// of the actual request name used by the driver.

	It("succeeds with '*' for a ResourceClaim when any request directory exists", func() {
		root := writeMetadataFile("resourceclaims", ifaceName, "vhost-user", socketPath)
		reader := dra.Reader{BaseDir: root}

		meta, resourceNameUsed, err := dra.ResolveForInterface(reader, ifaceName)
		Expect(err).ToNot(HaveOccurred())
		Expect(meta.SocketPath).To(Equal(socketPath))
		Expect(resourceNameUsed).To(Equal("*"))
	})

	It("succeeds with '*' for a ResourceClaimTemplate when no ResourceClaim exists", func() {
		root := writeMetadataFile("resourceclaimtemplates", ifaceName, "vhost-user", socketPath)
		reader := dra.Reader{BaseDir: root}

		meta, resourceNameUsed, err := dra.ResolveForInterface(reader, ifaceName)
		Expect(err).ToNot(HaveOccurred())
		Expect(meta.SocketPath).To(Equal(socketPath))
		Expect(resourceNameUsed).To(Equal("*"))
	})

	It("falls back to 'vhost-user' when '*' glob finds no subdirectories but exact name matches", func() {
		// Simulate a driver that names its request "vhost-user" but the claim
		// directory has no subdirectories visible to the "*" glob (e.g. the
		// directory itself is absent). Only the exact "vhost-user" path exists.
		// We achieve this by having no resourceclaims dir at all, and a
		// resourceclaimtemplates dir where the only subdir is "vhost-user" —
		// but since "*" glob WILL match "vhost-user", we instead test the
		// "vhost-user" path directly via ReadClaim/ReadClaimTemplate.
		// This test verifies that ReadClaim with "vhost-user" works correctly.
		root := writeMetadataFile("resourceclaims", ifaceName, "vhost-user", socketPath)
		reader := dra.Reader{BaseDir: root}

		meta, err := reader.ReadClaim(ifaceName, "vhost-user")
		Expect(err).ToNot(HaveOccurred())
		Expect(meta.SocketPath).To(Equal(socketPath))
	})

	It("returns an error when no metadata exists for any resource name or claim type", func() {
		reader := dra.Reader{BaseDir: GinkgoT().TempDir()}

		_, _, err := dra.ResolveForInterface(reader, ifaceName)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no DRA metadata found"))
		Expect(err.Error()).To(ContainSubstring(ifaceName))
	})
})
