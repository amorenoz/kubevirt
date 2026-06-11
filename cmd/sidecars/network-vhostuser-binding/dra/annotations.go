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
	"strings"
)

// AnnotationPrefix is the prefix for VMI annotations that map interface names
// to DRA resource claims.
// Format: resource.vhost-user-binding-plugin.io/<ifaceName>: <podClaimName>/<requestName>
const AnnotationPrefix = "resource.vhost-user-binding-plugin.io/"

// ResourceRef holds the DRA pod claim name and request name for an interface.
type ResourceRef struct {
	PodClaimName string
	RequestName  string
}

// ParseAnnotations extracts DRA resource references from VMI annotations,
// returning a map of interface name → ResourceRef.
// Annotations not matching [AnnotationPrefix] are ignored.
func ParseAnnotations(annotations map[string]string) (map[string]ResourceRef, error) {
	result := make(map[string]ResourceRef)
	for key, value := range annotations {
		ifaceName, found := strings.CutPrefix(key, AnnotationPrefix)
		if !found || ifaceName == "" {
			continue
		}
		podClaimName, requestName, found := strings.Cut(value, "/")
		if !found || podClaimName == "" || requestName == "" {
			return nil, fmt.Errorf("annotation %q has invalid value %q: expected <podClaimName>/<requestName>", key, value)
		}
		result[ifaceName] = ResourceRef{
			PodClaimName: podClaimName,
			RequestName:  requestName,
		}
	}
	return result, nil
}
