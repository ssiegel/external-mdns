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

package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"

	"github.com/blake/external-mdns/mdns"
	"github.com/blake/external-mdns/resource"
	"github.com/blake/external-mdns/source"
	"github.com/miekg/dns"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/informers"
)

type k8sSource []string

func (s *k8sSource) String() string {
	return fmt.Sprint(*s)
}

func (s *k8sSource) Set(value string) error {
	switch value {
	case "ingress", "service":
		*s = append(*s, value)
	}
	return nil
}

/*
The following functions were obtained from
https://www.gmarik.info/blog/2019/12-factor-golang-flag-package/

	- getConfig()
	- lookupEnvOrInt()
	- lookupEnvOrString()
*/

func getConfig(fs *flag.FlagSet) []string {
	cfg := make([]string, 0, 10)
	fs.VisitAll(func(f *flag.Flag) {
		cfg = append(cfg, fmt.Sprintf("%s:%q", f.Name, f.Value.String()))
	})

	return cfg
}

func lookupEnvOrString(key string, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return defaultVal
}

func lookupEnvOrInt(key string, defaultVal int) int {
	if val, ok := os.LookupEnv(key); ok {
		v, err := strconv.Atoi(val)
		if err != nil {
			log.Fatalf("lookupEnvOrInt[%s]: %v", key, err)
		}
		return v
	}
	return defaultVal
}

func lookupEnvOrBool(key string, defaultVal bool) bool {
	if val, ok := os.LookupEnv(key); ok {
		v, err := strconv.ParseBool(val)
		if err != nil {
			log.Fatalf("lookupEnvOrBool[%s]: %v", key, err)
		}
		return v
	}
	return defaultVal
}

var (
	master           = ""
	namespace        = ""
	publishAll       = false
	test             = flag.Bool("test", false, "testing mode, no connection to k8s")
	sourceFlag       k8sSource
	kubeconfig       string
	recordTTL        = 120
)

func main() {

	// Kubernetes options
	flag.StringVar(&kubeconfig, "kubeconfig", lookupEnvOrString("EXTERNAL_MDNS_KUBECONFIG", kubeconfigPath()), "(optional) Absolute path to the kubeconfig file")
	flag.StringVar(&master, "master", lookupEnvOrString("EXTERNAL_MDNS_MASTER", master), "URL to Kubernetes master")

	// External-mDNS options
	flag.BoolVar(&publishAll, "publish-all", lookupEnvOrBool("EXTERNAL_MDNS_PUBLISH_ALL", publishAll), "Published all services, including those without annotation (default: false)")
	flag.StringVar(&namespace, "namespace", lookupEnvOrString("EXTERNAL_MDNS_NAMESPACE", namespace), "Limit sources of endpoints to a specific namespace (default: all namespaces)")
	flag.Var(&sourceFlag, "source", "The resource types that are queried for endpoints; specify multiple times for multiple sources (required, options: service, ingress)")
	flag.IntVar(&recordTTL, "record-ttl", lookupEnvOrInt("EXTERNAL_MDNS_RECORD_TTL", recordTTL), "DNS record time-to-live")

	flag.Parse()

	if *test {
		mdns.Publish(&dns.A{Hdr: dns.RR_Header{Name: "router.local.", Ttl: uint32(recordTTL), Class: dns.ClassINET, Rrtype: dns.TypeA}, A: net.ParseIP("192.168.1.254")})
		mdns.UnPublish(&dns.PTR{Hdr: dns.RR_Header{Name: "254.1.168.192.in-addr.arpa.", Ttl: uint32(recordTTL), Class: dns.ClassINET, Rrtype: dns.TypePTR}, Ptr: "router.local."})

		select {}
	}

	// No sources provided.
	if len(sourceFlag) == 0 {
		fmt.Println("Specify at least once source to sync records from.")
		os.Exit(1)
	}

	// Print parsed configuration
	log.Printf("app.config %v\n", getConfig(flag.CommandLine))

	k8sClient, err := newK8sClient()
	if err != nil {
		log.Fatalln("Failed to create Kubernetes client:", err)
	}

	notifyMdns := make(chan resource.Resource)
	stopper := make(chan struct{})
	defer close(stopper)
	defer runtime.HandleCrash()

	factory := informers.NewSharedInformerFactory(k8sClient, 0)
	for _, src := range sourceFlag {
		switch src {
		case "ingress":
			ingressController := source.NewIngressWatcher(factory, namespace, notifyMdns)
			go ingressController.Run(stopper)
		case "service":
			serviceController := source.NewServicesWatcher(factory, publishAll, notifyMdns)
			go serviceController.Run(stopper)
		}
	}

	for {
		select {
		case advertiseResource := <-notifyMdns:
			for _, record := range advertiseResource.Records {
				record.Header().Ttl = uint32(recordTTL)
				record.Header().Class = dns.ClassINET
				switch advertiseResource.Action {
				case resource.Added:
					mdns.Publish(record)
				case resource.Deleted:
					mdns.UnPublish(record)
				}
			}
		case <-stopper:
			fmt.Println("Stopping program")
		}
	}
}
