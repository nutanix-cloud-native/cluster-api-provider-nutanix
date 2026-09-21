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
	"fmt"
	"net/http"

	prismresponse "github.com/nutanix/ntnx-api-golang-clients/prism-go-client/v4/models/common/v1/response"
	prismconfig "github.com/nutanix/ntnx-api-golang-clients/prism-go-client/v4/models/prism/v4/config"
	"k8s.io/utils/ptr"
)

func (s *Simulator) registerPrismRoutes() {
	m := s.mux
	m.HandleFunc("GET /api/prism/{version}/config/categories", s.handleListCategories)
	m.HandleFunc("POST /api/prism/{version}/config/categories", s.handleCreateCategory)
	m.HandleFunc("GET /api/prism/{version}/config/categories/{extId}", s.handleGetCategory)
	m.HandleFunc("DELETE /api/prism/{version}/config/categories/{extId}", s.handleDeleteCategory)
	m.HandleFunc("GET /api/prism/{version}/config/tasks", s.handleListTasks)
	m.HandleFunc("GET /api/prism/{version}/config/tasks/{extId}", s.handleGetTask)
}

func prismMetadata(total int) *prismresponse.ApiResponseMetadata {
	md := prismresponse.NewApiResponseMetadata()
	md.TotalAvailableResults = ptr.To(total)
	return md
}

// --- Categories ---

func (s *Simulator) handleListCategories(w http.ResponseWriter, r *http.Request) {
	q, err := parseListQuery(r)
	if err != nil {
		writeBadRequest(w, err.Error())
		return
	}
	s.store.mu.RLock()
	defer s.store.mu.RUnlock()
	records := make([]*categoryRecord, 0, len(s.store.categories))
	for _, rec := range s.store.categories {
		records = append(records, rec)
	}
	page, total, err := selectPage(q, records, func(rec *categoryRecord) (map[string]any, error) { return rec.doc.get(rec.category) })
	if err != nil {
		writeBadRequest(w, err.Error())
		return
	}
	categories := make([]prismconfig.Category, 0, len(page))
	for _, rec := range page {
		setETag(rec.category, etagFor(rec.version))
		categories = append(categories, *rec.category)
	}
	resp := prismconfig.NewListCategoriesApiResponse()
	resp.Metadata = prismMetadata(total)
	if err := setListData(resp, categories); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, "", resp)
}

func (s *Simulator) handleGetCategory(w http.ResponseWriter, r *http.Request) {
	s.store.mu.RLock()
	defer s.store.mu.RUnlock()
	rec, ok := s.store.categories[r.PathValue("extId")]
	if !ok {
		writeNotFound(w, "category", r.PathValue("extId"))
		return
	}
	etag := etagFor(rec.version)
	setETag(rec.category, etag)
	resp := prismconfig.NewGetCategoryApiResponse()
	if err := resp.SetData(*rec.category); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, etag, resp)
}

func (s *Simulator) handleCreateCategory(w http.ResponseWriter, r *http.Request) {
	in := prismconfig.NewCategory()
	if !decodeBody(w, r, in) {
		return
	}
	if in.Key == nil || *in.Key == "" || in.Value == nil || *in.Value == "" {
		writeBadRequest(w, "category key and value are required")
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	if existing := s.store.findCategory(*in.Key, *in.Value); existing != nil {
		writeError(w, http.StatusConflict, "CATEGORY_ALREADY_EXISTS",
			fmt.Sprintf("category %s:%s already exists with extId %s", *in.Key, *in.Value, *existing.category.ExtId))
		return
	}
	rec := s.store.addCategory(*in.Key, *in.Value)
	if in.Description != nil {
		rec.category.Description = in.Description
	}
	etag := etagFor(rec.version)
	setETag(rec.category, etag)
	resp := prismconfig.NewCreateCategoryApiResponse()
	if err := resp.SetData(*rec.category); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, etag, resp)
}

func (s *Simulator) handleDeleteCategory(w http.ResponseWriter, r *http.Request) {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	rec, ok := s.store.categories[r.PathValue("extId")]
	if !ok {
		writeNotFound(w, "category", r.PathValue("extId"))
		return
	}
	if !checkIfMatch(w, r, etagFor(rec.version)) {
		return
	}
	delete(s.store.categories, *rec.category.ExtId)
	w.WriteHeader(http.StatusNoContent)
}

// --- Tasks ---

func (s *Simulator) handleListTasks(w http.ResponseWriter, r *http.Request) {
	q, err := parseListQuery(r)
	if err != nil {
		writeBadRequest(w, err.Error())
		return
	}
	s.store.mu.RLock()
	defer s.store.mu.RUnlock()
	records := make([]*taskRecord, 0, len(s.store.tasks))
	for _, rec := range s.store.tasks {
		records = append(records, rec)
	}
	page, total, err := selectPage(q, records, func(rec *taskRecord) (map[string]any, error) { return rec.doc.get(rec.task) })
	if err != nil {
		writeBadRequest(w, err.Error())
		return
	}
	tasks := make([]prismconfig.Task, 0, len(page))
	for _, rec := range page {
		tasks = append(tasks, *rec.task)
	}
	resp := prismconfig.NewListTasksApiResponse()
	resp.Metadata = prismMetadata(total)
	if err := setListData(resp, tasks); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, "", resp)
}

func (s *Simulator) handleGetTask(w http.ResponseWriter, r *http.Request) {
	s.store.mu.RLock()
	defer s.store.mu.RUnlock()
	rec, ok := s.store.tasks[r.PathValue("extId")]
	if !ok {
		writeNotFound(w, "task", r.PathValue("extId"))
		return
	}
	resp := prismconfig.NewGetTaskApiResponse()
	if err := resp.SetData(*rec.task); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, etagFor(1), resp)
}
