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
	"strings"

	"github.com/blake/external-mdns/resource"
	"github.com/miekg/dns"
	v1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

// IngressSource handles adding, updating, or removing mDNS record advertisements
type IngressSource struct {
	namespace      string
	notifyChan     chan<- resource.Resource
	sharedInformer cache.SharedIndexInformer
}

// Run starts shared informers and waits for the shared informer cache to
// synchronize.
func (i *IngressSource) Run(stopCh chan struct{}) error {
	i.sharedInformer.Run(stopCh)
	if !cache.WaitForCacheSync(stopCh, i.sharedInformer.HasSynced) {
		runtime.HandleError(fmt.Errorf("timed out waiting for caches to sync"))
	}
	return nil
}

func (i *IngressSource) onAdd(obj interface{}) {
	i.notifyChan <- resource.Resource {
		SourceType: "ingress",
		Action:     resource.Added,
		Records:    i.buildRecords(obj),
	}
}

func (i *IngressSource) onDelete(obj interface{}) {
	i.notifyChan <- resource.Resource {
		SourceType: "ingress",
		Action:     resource.Deleted,
		Records:    i.buildRecords(obj),
	}
}

func (i *IngressSource) onUpdate(oldObj interface{}, newObj interface{}) {
	i.onDelete(oldObj)
	i.onAdd(newObj)
}

func (i *IngressSource) buildRecords(obj interface{}) []dns.RR {
	var records []dns.RR

	ingress, ok := obj.(*v1.Ingress)
	if !ok {
		return records
	}

	var ip net.IP
	for _, lb := range ingress.Status.LoadBalancer.Ingress {
		if lb.IP != "" {
			ip = net.ParseIP(lb.IP)
		}
	}

	if ip == nil {
		return records
	}

        if i.namespace != "" && i.namespace != ingress.Namespace {
                return records
        }

	// Advertise each hostname under this Ingress
	for _, rule := range ingress.Spec.Rules {
		// Skip rules with no hostname or that do not use the .local TLD
		if rule.Host != "" && strings.HasSuffix(rule.Host, ".local") {
			records = append(records, buildARecord(fmt.Sprintf("%s.", rule.Host), ip, false)...)
		}
	}

	return records
}

// NewIngressWatcher creates an IngressSource
func NewIngressWatcher(factory informers.SharedInformerFactory, namespace string, notifyChan chan<- resource.Resource) IngressSource {
	ingressInformer := factory.Networking().V1().Ingresses().Informer()
	i := &IngressSource{
		namespace:      namespace,
		notifyChan:     notifyChan,
		sharedInformer: ingressInformer,
	}

	ingressInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    i.onAdd,
		DeleteFunc: i.onDelete,
		UpdateFunc: i.onUpdate,
	})

	return *i
}
