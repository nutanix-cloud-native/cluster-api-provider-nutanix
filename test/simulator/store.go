/*
Copyright 2026 Nutanix

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

package simulator

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"
	clustermgmtconfig "github.com/nutanix/ntnx-api-golang-clients/clustermgmt-go-client/v4/models/clustermgmt/v4/config"
	networkingconfig "github.com/nutanix/ntnx-api-golang-clients/networking-go-client/v4/models/networking/v4/config"
	prismconfig "github.com/nutanix/ntnx-api-golang-clients/prism-go-client/v4/models/prism/v4/config"
	vmmconfig "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/ahv/config"
	vmmcontent "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/content"
	"k8s.io/utils/ptr"
)

// cachedDoc memoises the JSON-document form of an entity, which is what the
// $filter evaluator consumes. Callers invalidate it whenever the entity
// changes.
type cachedDoc struct {
	doc map[string]any
}

func (c *cachedDoc) get(entity any) (map[string]any, error) {
	if c.doc != nil {
		return c.doc, nil
	}
	raw, err := json.Marshal(entity)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	c.doc = doc
	return doc, nil
}

func (c *cachedDoc) invalidate() { c.doc = nil }

type vmRecord struct {
	vm      *vmmconfig.Vm
	version uint64
	doc     cachedDoc
	// ips maps NIC extId to the address allocated from the subnet pool.
	ips map[string]allocatedIP
}

func (r *vmRecord) touch() {
	r.version++
	r.vm.UpdateTime = ptr.To(time.Now().UTC())
	r.doc.invalidate()
}

type allocatedIP struct {
	subnetExtID string
	ip          uint32
}

type categoryRecord struct {
	category *prismconfig.Category
	version  uint64
	doc      cachedDoc
}

type taskRecord struct {
	task *prismconfig.Task
	doc  cachedDoc
}

type seededEntity[T any] struct {
	entity *T
	doc    cachedDoc
}

// store is the in-memory Prism Central state. Every access goes through mu.
type store struct {
	mu sync.RWMutex

	clusters          []*seededEntity[clustermgmtconfig.Cluster]
	subnets           []*seededEntity[networkingconfig.Subnet]
	images            []*seededEntity[vmmcontent.Image]
	storageContainers []*seededEntity[clustermgmtconfig.StorageContainer]
	categories        map[string]*categoryRecord
	vms               map[string]*vmRecord
	tasks             map[string]*taskRecord
	pools             map[string]*ipPool
}

func newStore(seed Seed) (*store, error) {
	s := &store{
		categories: map[string]*categoryRecord{},
		vms:        map[string]*vmRecord{},
		tasks:      map[string]*taskRecord{},
		pools:      map[string]*ipPool{},
	}
	if len(seed.Clusters) == 0 {
		return nil, fmt.Errorf("seed must contain at least one cluster")
	}
	for _, c := range seed.Clusters {
		s.clusters = append(s.clusters, &seededEntity[clustermgmtconfig.Cluster]{entity: newSeedCluster(c)})
	}
	for _, sn := range seed.Subnets {
		if err := s.seedSubnet(sn); err != nil {
			return nil, err
		}
	}
	for _, img := range seed.Images {
		s.images = append(s.images, &seededEntity[vmmcontent.Image]{entity: newSeedImage(img, s.clusterExtIDs())})
	}
	for _, sc := range seed.StorageContainers {
		cluster, err := s.resolveCluster(sc.Cluster)
		if err != nil {
			return nil, fmt.Errorf("storage container %s: %w", sc.Name, err)
		}
		s.storageContainers = append(s.storageContainers,
			&seededEntity[clustermgmtconfig.StorageContainer]{entity: newSeedStorageContainer(sc, cluster)})
	}
	for _, c := range seed.Categories {
		s.addCategory(c.Key, c.Value)
	}
	return s, nil
}

func orUUID(id string) string {
	if id == "" {
		return uuid.NewString()
	}
	return id
}

func newSeedCluster(c ClusterSeed) *clustermgmtconfig.Cluster {
	cluster := clustermgmtconfig.NewCluster()
	cluster.ExtId = ptr.To(orUUID(c.UUID))
	cluster.Name = ptr.To(c.Name)
	cluster.Config = clustermgmtconfig.NewClusterConfigReference()
	cluster.Config.ClusterFunction = []clustermgmtconfig.ClusterFunctionRef{clustermgmtconfig.CLUSTERFUNCTIONREF_AOS}
	cluster.Config.HypervisorTypes = []clustermgmtconfig.HypervisorType{clustermgmtconfig.HYPERVISORTYPE_AHV}
	cluster.Config.IsAvailable = ptr.To(true)
	return cluster
}

func (s *store) seedSubnet(sn SubnetSeed) error {
	cluster, err := s.resolveCluster(sn.Cluster)
	if err != nil {
		return fmt.Errorf("subnet %s: %w", sn.Name, err)
	}
	pool, err := newIPPool(sn.CIDR, sn.Start, sn.End)
	if err != nil {
		return fmt.Errorf("subnet %s: %w", sn.Name, err)
	}
	subnet := networkingconfig.NewSubnet()
	subnet.ExtId = ptr.To(orUUID(sn.UUID))
	subnet.Name = ptr.To(sn.Name)
	subnet.SubnetType = ptr.To(networkingconfig.SUBNETTYPE_VLAN)
	subnet.ClusterReference = cluster.ExtId
	subnet.ClusterName = cluster.Name
	subnet.NetworkId = ptr.To(sn.VlanID)
	s.subnets = append(s.subnets, &seededEntity[networkingconfig.Subnet]{entity: subnet})
	s.pools[*subnet.ExtId] = pool
	return nil
}

func newSeedImage(img ImageSeed, clusterExtIDs []string) *vmmcontent.Image {
	image := vmmcontent.NewImage()
	image.ExtId = ptr.To(orUUID(img.UUID))
	image.Name = ptr.To(img.Name)
	image.Type = ptr.To(vmmcontent.IMAGETYPE_DISK_IMAGE)
	image.SizeBytes = ptr.To(img.SizeBytes)
	image.CreateTime = ptr.To(time.Now().UTC())
	image.ClusterLocationExtIds = clusterExtIDs
	return image
}

func newSeedStorageContainer(sc StorageContainerSeed, cluster *clustermgmtconfig.Cluster) *clustermgmtconfig.StorageContainer {
	container := clustermgmtconfig.NewStorageContainer()
	id := orUUID(sc.UUID)
	container.ExtId = ptr.To(id)
	container.ContainerExtId = ptr.To(id)
	container.Name = ptr.To(sc.Name)
	container.ClusterExtId = cluster.ExtId
	container.ClusterName = cluster.Name
	return container
}

func (s *store) clusterExtIDs() []string {
	ids := make([]string, 0, len(s.clusters))
	for _, c := range s.clusters {
		ids = append(ids, *c.entity.ExtId)
	}
	return ids
}

// resolveCluster finds a seeded cluster by name or UUID; empty selects the
// first cluster.
func (s *store) resolveCluster(nameOrUUID string) (*clustermgmtconfig.Cluster, error) {
	if nameOrUUID == "" {
		return s.clusters[0].entity, nil
	}
	for _, c := range s.clusters {
		if *c.entity.ExtId == nameOrUUID || *c.entity.Name == nameOrUUID {
			return c.entity, nil
		}
	}
	return nil, fmt.Errorf("cluster %q not found", nameOrUUID)
}

func (s *store) clusterByExtID(extID string) *clustermgmtconfig.Cluster {
	for _, c := range s.clusters {
		if *c.entity.ExtId == extID {
			return c.entity
		}
	}
	return nil
}

func (s *store) subnetByExtID(extID string) *networkingconfig.Subnet {
	for _, sn := range s.subnets {
		if *sn.entity.ExtId == extID {
			return sn.entity
		}
	}
	return nil
}

func (s *store) imageByExtID(extID string) *vmmcontent.Image {
	for _, img := range s.images {
		if *img.entity.ExtId == extID {
			return img.entity
		}
	}
	return nil
}

func (s *store) addCategory(key, value string) *categoryRecord {
	category := prismconfig.NewCategory()
	category.ExtId = ptr.To(uuid.NewString())
	category.Key = ptr.To(key)
	category.Value = ptr.To(value)
	category.Type = ptr.To(prismconfig.CATEGORYTYPE_USER)
	rec := &categoryRecord{category: category, version: 1}
	s.categories[*category.ExtId] = rec
	return rec
}

func (s *store) findCategory(key, value string) *categoryRecord {
	for _, rec := range s.categories {
		if *rec.category.Key == key && *rec.category.Value == value {
			return rec
		}
	}
	return nil
}

// ipPool hands out addresses from a range inside a CIDR.
type ipPool struct {
	prefixLen int
	first     uint32
	last      uint32
	next      uint32
	used      map[uint32]struct{}
}

func newIPPool(cidr, start, end string) (*ipPool, error) {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("invalid cidr %q: %w", cidr, err)
	}
	if network.IP.To4() == nil {
		return nil, fmt.Errorf("cidr %q: only IPv4 is supported", cidr)
	}
	prefixLen, _ := network.Mask.Size()
	base := ipToUint32(network.IP)
	broadcast := base | ^binary.BigEndian.Uint32(network.Mask)
	first, last := base+10, broadcast-1
	if prefixLen >= 30 {
		first, last = base+1, broadcast-1
	}
	if start != "" {
		ip, err := parseIPv4(start)
		if err != nil {
			return nil, err
		}
		first = ip
	}
	if end != "" {
		ip, err := parseIPv4(end)
		if err != nil {
			return nil, err
		}
		last = ip
	}
	if first > last || !network.Contains(uint32ToIP(first)) || !network.Contains(uint32ToIP(last)) {
		return nil, fmt.Errorf("cidr %q: allocation range %s-%s is invalid", cidr, uint32ToIP(first), uint32ToIP(last))
	}
	return &ipPool{prefixLen: prefixLen, first: first, last: last, next: first, used: map[uint32]struct{}{}}, nil
}

func parseIPv4(s string) (uint32, error) {
	ip := net.ParseIP(s)
	if ip == nil || ip.To4() == nil {
		return 0, fmt.Errorf("invalid IPv4 address %q", s)
	}
	return ipToUint32(ip), nil
}

func ipToUint32(ip net.IP) uint32 { return binary.BigEndian.Uint32(ip.To4()) }

func uint32ToIP(n uint32) net.IP {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, n)
	return net.IP(b)
}

// allocate returns the next free address, scanning the whole range once.
func (p *ipPool) allocate() (uint32, bool) {
	size := p.last - p.first + 1
	for i := uint32(0); i < size; i++ {
		candidate := p.first + (p.next-p.first+i)%size
		if _, taken := p.used[candidate]; !taken {
			p.used[candidate] = struct{}{}
			p.next = candidate + 1
			return candidate, true
		}
	}
	return 0, false
}

func (p *ipPool) release(ip uint32) { delete(p.used, ip) }

func etagFor(version uint64) string { return fmt.Sprintf("\"%d\"", version) }
