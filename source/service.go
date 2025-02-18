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
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/blake/external-mdns/resource"
	"github.com/miekg/dns"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

// ServiceSource handles adding, updating, or removing mDNS record advertisements
type ServiceSource struct {
	publishAll       bool
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

	hostname, hasHostname := service.Annotations["external-mdns.blake.github.io/hostname"]
	if !hasHostname {
		hostname = fmt.Sprintf("%s.%s.local.", service.Name, service.Namespace)
	}

	instancename, hasInstancename := service.Annotations["external-mdns.blake.github.io/service-instance"]
	if !hasInstancename {
		instancename = fmt.Sprintf("%s/%s", service.Namespace, service.Name)
	}

	svctxt := map[string][]string{}
	txtstr, hasTxt := service.Annotations["external-mdns.blake.github.io/service-txt"]
	if txtstr != "" && hasTxt {
		var txtmap map[string]map[string]string
		if err := json.Unmarshal([]byte(txtstr), &txtmap); err == nil {
			for svc, txt := range txtmap {
				for k, v := range txt {
					svctxt[svc] = append(svctxt[svc], fmt.Sprintf("%s=%s", k, v))
				}
			}
		}
	}

	if !s.publishAll && !hasHostname && !hasInstancename && !hasTxt {
		_, hasPublish := service.Annotations["external-mdns.blake.github.io/publish"]
		if !hasPublish {
			return records
		}
	}

	var ip net.IP
	if service.Spec.Type == "ClusterIP" {
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

        if !strings.HasSuffix(hostname, ".") {
        	hostname = hostname + "."
        }
        if !strings.HasSuffix(hostname, ".local.") {
        	hostname = hostname + "local."
        }

	records = buildARecord(hostname, ip, true)
	for _, port := range service.Spec.Ports {
		records = append(records, buildSRVRecord(instancename, port.Name, port.Protocol, hostname, uint16(port.Port), svctxt[port.Name])...)
	}

	return records
}

// NewServicesWatcher creates an ServiceSource
func NewServicesWatcher(factory informers.SharedInformerFactory, publishAll bool, notifyChan chan<- resource.Resource) ServiceSource {
	servicesInformer := factory.Core().V1().Services().Informer()
	s := &ServiceSource{
		publishAll:      publishAll,
		notifyChan:      notifyChan,
		sharedInformer:  servicesInformer,
	}
	servicesInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    s.onAdd,
		DeleteFunc: s.onDelete,
		UpdateFunc: s.onUpdate,
	})

	return *s
}
