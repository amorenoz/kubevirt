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

package dra

import (
	"fmt"

	"kubevirt.io/client-go/log"
)

// ResolveForInterface resolves the socket metadata for a single interface
// using the explicit pod claim name and request name from the VMI annotation.
// It tries both a direct ResourceClaim and a ResourceClaimTemplate, logging
// the outcome of every attempt.
func ResolveForInterface(reader Reader, ifaceName string, ref ResourceRef) (SocketMetadata, error) {
	type attempt struct {
		claimType string
		readFn    func() (SocketMetadata, error)
	}
	attempts := []attempt{
		{"ResourceClaim", func() (SocketMetadata, error) {
			return reader.ReadClaim(ref.PodClaimName, ref.RequestName)
		}},
		{"ResourceClaimTemplate", func() (SocketMetadata, error) {
			return reader.ReadClaimTemplate(ref.PodClaimName, ref.RequestName)
		}},
	}

	for _, a := range attempts {
		log.Log.Infof("DRA: interface=%q %s podClaimName=%q requestName=%q: attempting lookup",
			ifaceName, a.claimType, ref.PodClaimName, ref.RequestName)

		meta, err := a.readFn()
		if err == nil {
			log.Log.Infof("DRA: interface=%q %s podClaimName=%q requestName=%q: SUCCESS socketPath=%q",
				ifaceName, a.claimType, ref.PodClaimName, ref.RequestName, meta.SocketPath)
			return meta, nil
		}

		log.Log.Warningf("DRA: interface=%q %s podClaimName=%q requestName=%q: FAILED: %v",
			ifaceName, a.claimType, ref.PodClaimName, ref.RequestName, err)
	}

	return SocketMetadata{}, fmt.Errorf("no DRA metadata found for interface %q (podClaimName=%q requestName=%q): tried ResourceClaim and ResourceClaimTemplate",
		ifaceName, ref.PodClaimName, ref.RequestName)
}
