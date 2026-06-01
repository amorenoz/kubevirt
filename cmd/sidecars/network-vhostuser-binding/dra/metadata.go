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

// Package dra provides DRA (Dynamic Resource Allocation) metadata reading
// for the vhost-user binding plugin.
//
// The central type is [Reader], which reads device metadata files from a
// configurable base directory. In production the base directory is
// [ContainerDir] (the well-known KEP-5304 mount point). Tests override it
// with a temporary directory containing pre-created metadata files.
package dra

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/dynamic-resource-allocation/api/metadata"
	"k8s.io/dynamic-resource-allocation/devicemetadata"
	"kubevirt.io/client-go/log"
)

// ContainerDir is the well-known in-container directory where the kubelet
// mounts DRA device metadata files (KEP-5304). Exported so that callers can
// check for its presence without importing the DRA library directly.
const ContainerDir = metadata.ContainerDir

// SocketAttributeKey is the device attribute key that the vhost-user DRA
// driver uses to publish the socket path.
const SocketAttributeKey = "socketPath"

// SocketMetadata is the minimal data the vhost-user binding plugin needs
// from a DRA device allocation.
type SocketMetadata struct {
	// SocketPath is the absolute path to the vhost-user socket file.
	SocketPath string
}

// Reader reads DRA device metadata files from BaseDir.
//
// In production, leave BaseDir empty and it defaults to [ContainerDir].
// In tests, set BaseDir to a temporary directory containing pre-created
// metadata files with the same layout as the real DRA mount:
//
//	<BaseDir>/resourceclaims/<claimName>/<requestName>/<driver>-metadata.json
//	<BaseDir>/resourceclaimtemplates/<podClaimName>/<requestName>/<driver>-metadata.json
type Reader struct {
	// BaseDir overrides the default [ContainerDir]. Leave empty in production.
	BaseDir string
}

func (r Reader) baseDir() string {
	if r.BaseDir != "" {
		return r.BaseDir
	}
	return ContainerDir
}

// ReadClaim reads metadata for a directly-referenced ResourceClaim.
// claimName must equal the interface name (strict-validation requirement).
// requestName is the DRA request name within the claim (e.g. "*" or "vhost-user").
func (r Reader) ReadClaim(claimName, requestName string) (SocketMetadata, error) {
	dir := filepath.Join(r.baseDir(), metadata.ResourceClaimsSubDir, claimName, requestName)
	return readDir(dir)
}

// ReadClaimTemplate reads metadata for a ResourceClaimTemplate-generated claim.
// podClaimName must equal the interface name (strict-validation requirement).
// requestName is tried in the same order as ReadClaim.
func (r Reader) ReadClaimTemplate(podClaimName, requestName string) (SocketMetadata, error) {
	dir := filepath.Join(r.baseDir(), metadata.ResourceClaimTemplatesSubDir, podClaimName, requestName)
	return readDir(dir)
}

// readDir globs for "*-metadata.json" files in dir and decodes the first one found.
func readDir(dir string) (SocketMetadata, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*"+metadata.MetadataFileSuffix))
	if err != nil {
		return SocketMetadata{}, fmt.Errorf("glob metadata files in %s: %w", dir, err)
	}
	if len(matches) == 0 {
		return SocketMetadata{}, fmt.Errorf("no metadata files found in %s", dir)
	}
	if len(matches) > 1 {
		log.Log.Warningf("more than one resource!")
	}
	return readSocketMetadataFromFile(matches[0])
}

// extractSocketMetadata pulls the socket path out of a decoded DeviceMetadata.
func extractSocketMetadata(dm *metadata.DeviceMetadata) (SocketMetadata, error) {
	for _, req := range dm.Requests {
		for _, dev := range req.Devices {
			attr, ok := dev.Attributes[SocketAttributeKey]
			if !ok {
				continue
			}
			if attr.StringValue == nil {
				return SocketMetadata{}, fmt.Errorf("attribute %q is not a string", SocketAttributeKey)
			}
			return SocketMetadata{SocketPath: *attr.StringValue}, nil
		}
	}
	return SocketMetadata{}, fmt.Errorf("no device with attribute %q found in metadata", SocketAttributeKey)
}

// readSocketMetadataFromFile decodes a single metadata JSON file and extracts
// the socket path.
func readSocketMetadataFromFile(path string) (SocketMetadata, error) {
	f, err := os.Open(path)
	if err != nil {
		return SocketMetadata{}, fmt.Errorf("open metadata file %s: %w", path, err)
	}
	defer f.Close()

	var dm metadata.DeviceMetadata
	if err := devicemetadata.DecodeMetadataFromStream(json.NewDecoder(f), &dm); err != nil {
		return SocketMetadata{}, fmt.Errorf("decode metadata file %s: %w", path, err)
	}
	return extractSocketMetadata(&dm)
}
