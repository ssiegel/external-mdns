// Copyright 2020 Blake Covarrubias
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package source

import (
	"fmt"
	"net"

	"github.com/blake/external-mdns/resource"
	"github.com/miekg/dns"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

// ServiceSource handles adding, updating, or removing mDNS record advertisements
type ServiceSource struct {
	defaultNamespace string
	withoutNamespace bool
	namespace        string
	publishInternal  bool
	notifyChan       chan<- resource.Resource
	sharedInformer   cache.SharedIndexInformer
}

// Run starts shared informers and waits for the shared informer cache to
// synchronize.
func (s *ServiceSource) Run(stopCh chan struct{}) error {
	s.sharedInformer.Run(stopCh)
	if !cache.WaitForCacheSync(stopCh, s.sharedInformer.HasSynced) {
		runtime.HandleError(fmt.Errorf("timed out waiting for caches to sync"))
	}
	return nil
}

func (s *ServiceSource) onAdd(obj interface{}) {
	s.notifyChan <- resource.Resource {
		SourceType: "service",
		Action:     resource.Added,
		Records:    s.buildRecords(obj),
	}
}

func (s *ServiceSource) onDelete(obj interface{}) {
	s.notifyChan <- resource.Resource {
		SourceType: "service",
		Action:     resource.Deleted,
		Records:    s.buildRecords(obj),
	}
}

func (s *ServiceSource) onUpdate(oldObj interface{}, newObj interface{}) {
	s.onDelete(oldObj)
	s.onAdd(newObj)
}

func (s *ServiceSource) buildRecords(obj interface{}) []dns.RR {
	var records []dns.RR

	service, ok := obj.(*corev1.Service)
	if !ok {
		return records
	}

	var ip net.IP
	if service.Spec.Type == "ClusterIP" && s.publishInternal {
		ip = net.ParseIP(service.Spec.ClusterIP)
	} else if service.Spec.Type == "LoadBalancer" {
		for _, lb := range service.Status.LoadBalancer.Ingress {
			if lb.IP != "" {
				ip = net.ParseIP(lb.IP)
			}
		}
	}

	if ip == nil {
		return records
	}

	if s.namespace != "" && s.namespace != service.Namespace {
		return records
	}

	records = buildARecord(fmt.Sprintf("%s.%s.local.", service.Name, service.Namespace), ip, true)
	if service.Namespace == s.defaultNamespace || s.withoutNamespace {
		records = append(records, buildARecord(fmt.Sprintf("%s.local.", service.Name), ip, false)...)
	}

	return records
}

// NewServicesWatcher creates an ServiceSource
func NewServicesWatcher(factory informers.SharedInformerFactory, defaultNamespace string, withoutNamespace bool, namespace string, notifyChan chan<- resource.Resource, publishInternal *bool) ServiceSource {
	servicesInformer := factory.Core().V1().Services().Informer()
	s := &ServiceSource{
		defaultNamespace: defaultNamespace,
		withoutNamespace: withoutNamespace,
		namespace:        namespace,
		publishInternal:  *publishInternal,
		notifyChan:       notifyChan,
		sharedInformer:   servicesInformer,
	}
	servicesInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    s.onAdd,
		DeleteFunc: s.onDelete,
		UpdateFunc: s.onUpdate,
	})

	return *s
}
