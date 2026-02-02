/*
Copyright The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v0alpha0

import (
	"k8s.io/apimachinery/pkg/labels"
	apiv0alpha0 "sigs.k8s.io/wg-ai-gateway/api/v0alpha0"
)

// BackendLister provides backward compatibility wrapper for XBackendDestinationLister
type BackendLister interface {
	List(selector labels.Selector) (ret []*apiv0alpha0.Backend, err error)
	Backends(namespace string) BackendNamespaceLister
}

// BackendNamespaceLister provides backward compatibility wrapper for XBackendDestinationNamespaceLister
type BackendNamespaceLister interface {
	List(selector labels.Selector) (ret []*apiv0alpha0.Backend, err error)
	Get(name string) (*apiv0alpha0.Backend, error)
}

// backendListerAdapter adapts XBackendDestinationLister to BackendLister
type backendListerAdapter struct {
	XBackendDestinationLister
}

// NewBackendLister creates a BackendLister from an XBackendDestinationLister
func NewBackendLister(lister XBackendDestinationLister) BackendLister {
	return &backendListerAdapter{XBackendDestinationLister: lister}
}

// Backends wraps XBackendDestinations to provide backward compatibility
func (b *backendListerAdapter) Backends(namespace string) BackendNamespaceLister {
	return &backendNamespaceListerAdapter{
		XBackendDestinationNamespaceLister: b.XBackendDestinations(namespace),
	}
}

// backendNamespaceListerAdapter adapts XBackendDestinationNamespaceLister to BackendNamespaceLister
type backendNamespaceListerAdapter struct {
	XBackendDestinationNamespaceLister
}
