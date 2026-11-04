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
	"net/http"

	clustermgmtconfig "github.com/nutanix/ntnx-api-golang-clients/clustermgmt-go-client/v4/models/clustermgmt/v4/config"
	clustermgmtresponse "github.com/nutanix/ntnx-api-golang-clients/clustermgmt-go-client/v4/models/common/v1/response"
	networkingresponse "github.com/nutanix/ntnx-api-golang-clients/networking-go-client/v4/models/common/v1/response"
	networkingconfig "github.com/nutanix/ntnx-api-golang-clients/networking-go-client/v4/models/networking/v4/config"
	"k8s.io/utils/ptr"
)

func (s *Simulator) registerClusterMgmtRoutes() {
	m := s.mux
	m.HandleFunc("GET /api/clustermgmt/{version}/config/clusters", s.handleListClusters)
	m.HandleFunc("GET /api/clustermgmt/{version}/config/clusters/{extId}", s.handleGetCluster)
	m.HandleFunc("GET /api/clustermgmt/{version}/config/storage-containers", s.handleListStorageContainers)
}

func (s *Simulator) registerNetworkingRoutes() {
	m := s.mux
	m.HandleFunc("GET /api/networking/{version}/config/subnets", s.handleListSubnets)
	m.HandleFunc("GET /api/networking/{version}/config/subnets/{extId}", s.handleGetSubnet)
}

// seededPage filters and pages a seeded entity list. The store lock must be
// held.
func seededPage[T any](w http.ResponseWriter, r *http.Request, items []*seededEntity[T]) ([]T, int, bool) {
	q, err := parseListQuery(r)
	if err != nil {
		writeBadRequest(w, err.Error())
		return nil, 0, false
	}
	page, total, err := selectPage(q, items, func(e *seededEntity[T]) (map[string]any, error) { return e.doc.get(e.entity) })
	if err != nil {
		writeBadRequest(w, err.Error())
		return nil, 0, false
	}
	out := make([]T, 0, len(page))
	for _, e := range page {
		out = append(out, *e.entity)
	}
	return out, total, true
}

func (s *Simulator) handleListClusters(w http.ResponseWriter, r *http.Request) {
	s.store.mu.RLock()
	defer s.store.mu.RUnlock()
	clusters, total, ok := seededPage(w, r, s.store.clusters)
	if !ok {
		return
	}
	md := clustermgmtresponse.NewApiResponseMetadata()
	md.TotalAvailableResults = ptr.To(total)
	resp := clustermgmtconfig.NewListClustersApiResponse()
	resp.Metadata = md
	if err := setListData(resp, clusters); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, "", resp)
}

func (s *Simulator) handleGetCluster(w http.ResponseWriter, r *http.Request) {
	s.store.mu.RLock()
	defer s.store.mu.RUnlock()
	cluster := s.store.clusterByExtID(r.PathValue("extId"))
	if cluster == nil {
		writeNotFound(w, "cluster", r.PathValue("extId"))
		return
	}
	resp := clustermgmtconfig.NewGetClusterApiResponse()
	if err := resp.SetData(*cluster); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, etagFor(1), resp)
}

func (s *Simulator) handleListStorageContainers(w http.ResponseWriter, r *http.Request) {
	s.store.mu.RLock()
	defer s.store.mu.RUnlock()
	containers, total, ok := seededPage(w, r, s.store.storageContainers)
	if !ok {
		return
	}
	md := clustermgmtresponse.NewApiResponseMetadata()
	md.TotalAvailableResults = ptr.To(total)
	resp := clustermgmtconfig.NewListStorageContainersApiResponse()
	resp.Metadata = md
	if err := setListData(resp, containers); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, "", resp)
}

func (s *Simulator) handleListSubnets(w http.ResponseWriter, r *http.Request) {
	s.store.mu.RLock()
	defer s.store.mu.RUnlock()
	subnets, total, ok := seededPage(w, r, s.store.subnets)
	if !ok {
		return
	}
	md := networkingresponse.NewApiResponseMetadata()
	md.TotalAvailableResults = ptr.To(total)
	resp := networkingconfig.NewListSubnetsApiResponse()
	resp.Metadata = md
	if err := setListData(resp, subnets); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, "", resp)
}

func (s *Simulator) handleGetSubnet(w http.ResponseWriter, r *http.Request) {
	s.store.mu.RLock()
	defer s.store.mu.RUnlock()
	subnet := s.store.subnetByExtID(r.PathValue("extId"))
	if subnet == nil {
		writeNotFound(w, "subnet", r.PathValue("extId"))
		return
	}
	resp := networkingconfig.NewGetSubnetApiResponse()
	if err := resp.SetData(*subnet); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, etagFor(1), resp)
}
