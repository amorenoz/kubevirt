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

// resourceNames is the ordered list of DRA request names to try when
// resolving metadata for a vhost-user interface. The wildcard "*" is
// attempted first; if it finds nothing we fall back to the hardcoded
// "vhost-user" name.
var resourceNames = []string{"*", "vhost-user"}

// ResolveForInterface tries each resource name in [resourceNames] against
// both a direct ResourceClaim and a ResourceClaimTemplate, logging the
// outcome of every attempt so that callers can see at a glance whether "*"
// worked or whether the fallback to "vhost-user" was necessary.
//
// The claimName / podClaimName passed to the reader must equal the interface
// name (strict-validation requirement).
//
// On success it returns the resolved [SocketMetadata] and the resource name
// that worked. On failure it returns an error that aggregates all individual
// attempt errors.
func ResolveForInterface(reader Reader, ifaceName string) (SocketMetadata, string, error) {
	type attempt struct {
		claimType string
		readFn    func(resourceName string) (SocketMetadata, error)
	}
	attempts := []attempt{
		{"ResourceClaim", func(rn string) (SocketMetadata, error) {
			return reader.ReadClaim(ifaceName, rn)
		}},
		{"ResourceClaimTemplate", func(rn string) (SocketMetadata, error) {
			return reader.ReadClaimTemplate(ifaceName, rn)
		}},
	}

	var allErrs []error
	for _, rn := range resourceNames {
		for _, a := range attempts {
			log.Log.Infof("DRA: interface=%q %s requestName=%q: attempting lookup",
				ifaceName, a.claimType, rn)

			meta, err := a.readFn(rn)
			if err == nil {
				log.Log.Infof("DRA: interface=%q %s requestName=%q: SUCCESS socketPath=%q",
					ifaceName, a.claimType, rn, meta.SocketPath)
				return meta, rn, nil
			}

			log.Log.Warningf("DRA: interface=%q %s requestName=%q: FAILED: %v",
				ifaceName, a.claimType, rn, err)
			allErrs = append(allErrs, fmt.Errorf("%s requestName=%q: %w", a.claimType, rn, err))
		}
	}

	return SocketMetadata{}, "", fmt.Errorf("no DRA metadata found for interface %q (tried all resource names and claim types): %v",
		ifaceName, allErrs)
}
