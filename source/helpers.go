// Copyright 2023 Stefan Siegel
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

	"github.com/miekg/dns"
	corev1 "k8s.io/api/core/v1"
)

func buildARecord (name string, addr net.IP, addReverse bool) []dns.RR {
	var reverseIP strings.Builder
	var reverse dns.RR
	var forward dns.RR

        if len(addr.To4()) == net.IPv4len {
        	// IPv4
        	addr = addr.To4()
        	fmt.Fprintf(&reverseIP, "%d.%d.%d.%d.in-addr.arpa.", addr[3], addr[2], addr[1], addr[0])
		reverse = &dns.PTR{
			Hdr: dns.RR_Header{Name: reverseIP.String(), Rrtype: dns.TypePTR},
			Ptr: name,
		}
		forward = &dns.A{
			Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeA},
			A: addr,
		}
	} else {
		// IPv6
		addr = addr.To16()
		for i, _ := range addr {
			b := addr[len(addr)-1-i]
			fmt.Fprintf(&reverseIP, "%x.%x.", b&0x0f, (b&0xf0)>>4)
		}
		fmt.Fprintf(&reverseIP, "ip6.arpa.")
		reverse = &dns.PTR{
			Hdr: dns.RR_Header{Name: reverseIP.String(), Rrtype: dns.TypePTR},
			Ptr: name,
		}
		forward = &dns.AAAA{
			Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeAAAA},
			AAAA: addr,
		}
	}

	if addReverse {
		return []dns.RR{forward, reverse}
	} else {
		return []dns.RR{forward}
	}
}

func buildSRVRecord (instancename string, servicename string, protocol corev1.Protocol, hostname string, port uint16, txt []string) []dns.RR {
	if instancename == "" || servicename == "" || hostname == "" || port == 0 {
		return []dns.RR{}
	}

	if len(txt) == 0 {
		txt = []string{""}
	}

	var proto string
	switch protocol {
	case corev1.ProtocolTCP:
		proto = "tcp"
	case corev1.ProtocolUDP:
		proto = "udp"
	default:
		return []dns.RR{}
	}

        dnsservice := fmt.Sprintf("_%s._%s.local.", strings.ToLower(servicename), proto)
        dnsinstance := fmt.Sprintf("%s.%s", instancename, dnsservice)

	return []dns.RR {
		&dns.PTR{
			Hdr: dns.RR_Header{Name: dnsservice, Rrtype: dns.TypePTR},
			Ptr: dnsinstance,
		},
	        &dns.SRV{
	        	Hdr: dns.RR_Header{Name: dnsinstance, Rrtype: dns.TypeSRV},
	        	Priority: 0,
	        	Weight: 0,
	        	Port: port,
	        	Target: hostname,
	        },
	        &dns.TXT{
	        	Hdr: dns.RR_Header{Name: dnsinstance, Rrtype: dns.TypeTXT},
	        	Txt: txt,
	        },
	}
}
