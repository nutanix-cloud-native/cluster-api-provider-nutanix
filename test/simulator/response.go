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
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"

	prismcommon "github.com/nutanix/ntnx-api-golang-clients/prism-go-client/v4/models/common/v1/config"
	prismerror "github.com/nutanix/ntnx-api-golang-clients/prism-go-client/v4/models/prism/v4/error"
	"k8s.io/utils/ptr"
)

const (
	headerETag    = "ETag"
	headerIfMatch = "If-Match"
	// defaultPageSize matches Prism Central's default $limit.
	defaultPageSize = 50
	maxPageSize     = 100
)

// setETag records etag in the $reserved map of an SDK model, which is where
// the SDK's GetEtag reads it from. entity must be a pointer to a model struct.
func setETag(entity any, etag string) {
	v := reflect.ValueOf(entity)
	if v.Kind() != reflect.Pointer || v.Elem().Kind() != reflect.Struct {
		return
	}
	field := v.Elem().FieldByName("Reserved_")
	if !field.IsValid() || !field.CanSet() || field.Kind() != reflect.Map {
		return
	}
	reserved := map[string]interface{}{headerETag: etag}
	if existing, ok := field.Interface().(map[string]interface{}); ok {
		for k, v := range existing {
			if k != headerETag {
				reserved[k] = v
			}
		}
	}
	field.Set(reflect.ValueOf(reserved))
}

// writeJSON writes body as JSON. When etag is set it is sent as a header and
// stamped into the envelope's $reserved so the SDK can find it either way.
func writeJSON(w http.ResponseWriter, status int, etag string, body any) {
	if etag != "" {
		setETag(body, etag)
		w.Header().Set(headerETag, etag)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"marshal response: %v"}`, err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

// errorEnvelope is the v4 error body: {"data": <prism.v4.error.ErrorResponse>}.
type errorEnvelope struct {
	Data *prismerror.ErrorResponse `json:"data"`
}

func newErrorResponse(errorGroup, code, message string) *prismerror.ErrorResponse {
	msg := prismerror.NewAppMessage()
	msg.ErrorGroup = ptr.To(errorGroup)
	msg.Code = ptr.To(code)
	msg.Message = ptr.To(message)
	msg.Severity = ptr.To(prismcommon.MESSAGESEVERITY_ERROR)
	resp := prismerror.NewErrorResponse()
	_ = resp.SetError([]prismerror.AppMessage{*msg})
	return resp
}

// writeError writes a v4-style error response. prism-go-client classifies
// 404 as NotFound, 429 as RateLimit and 5xx as Internal from the status
// alone; errorGroup is consulted for other statuses.
func writeError(w http.ResponseWriter, status int, errorGroup, message string) {
	writeJSON(w, status, "", errorEnvelope{Data: newErrorResponse(errorGroup, fmt.Sprintf("SIM-%d", status), message)})
}

func writeNotFound(w http.ResponseWriter, kind, extID string) {
	writeError(w, http.StatusNotFound, "ENTITY_NOT_FOUND", fmt.Sprintf("%s with extId %s not found", kind, extID))
}

func writeBadRequest(w http.ResponseWriter, message string) {
	writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", message)
}

func writePreconditionFailed(w http.ResponseWriter, message string) {
	writeError(w, http.StatusPreconditionFailed, "PRECONDITION_FAILED", message)
}

func writeNotImplemented(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED",
		fmt.Sprintf("ntnx-sim does not implement %s %s", r.Method, r.URL.Path))
}

// checkIfMatch validates the If-Match header against the entity's current
// ETag. Prism Central requires it on every mutation.
func checkIfMatch(w http.ResponseWriter, r *http.Request, current string) bool {
	got := r.Header.Get(headerIfMatch)
	if got == "" {
		writePreconditionFailed(w, "If-Match header is required")
		return false
	}
	if got != current && got != "*" {
		writePreconditionFailed(w, fmt.Sprintf("If-Match %s does not match current ETag %s", got, current))
		return false
	}
	return true
}

// decodeBody unmarshals a JSON request body into dst.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeBadRequest(w, fmt.Sprintf("invalid request body: %v", err))
		return false
	}
	return true
}

// listQuery holds the OData paging and filter parameters of a list request.
type listQuery struct {
	matcher *filterMatcher
	page    int
	limit   int
}

func parseListQuery(r *http.Request) (*listQuery, error) {
	q := r.URL.Query()
	matcher, err := newFilterMatcher(q.Get("$filter"))
	if err != nil {
		return nil, err
	}
	page, err := intParam(q.Get("$page"), 0)
	if err != nil {
		return nil, err
	}
	limit, err := intParam(q.Get("$limit"), defaultPageSize)
	if err != nil {
		return nil, err
	}
	if limit < 1 {
		limit = defaultPageSize
	}
	if limit > maxPageSize {
		limit = maxPageSize
	}
	return &listQuery{matcher: matcher, page: page, limit: limit}, nil
}

func intParam(raw string, def int) (int, error) {
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid integer parameter %q", raw)
	}
	return n, nil
}

// selectPage applies the filter to every item (via its cached JSON document)
// and returns the requested page plus the total number of matches.
func selectPage[T any](q *listQuery, items []T, docOf func(T) (map[string]any, error)) ([]T, int, error) {
	matched := make([]T, 0, len(items))
	for _, item := range items {
		ok := true
		if q.matcher.expr != nil {
			doc, err := docOf(item)
			if err != nil {
				return nil, 0, err
			}
			res, err := q.matcher.expr.eval(doc, nil)
			if err != nil {
				return nil, 0, err
			}
			ok = truthy(res)
		}
		if ok {
			matched = append(matched, item)
		}
	}
	total := len(matched)
	start := q.page * q.limit
	if start >= total {
		return []T{}, total, nil
	}
	end := start + q.limit
	if end > total {
		end = total
	}
	return matched[start:end], total, nil
}

// dataSetter is implemented by every SDK list response envelope.
type dataSetter interface {
	SetData(v interface{}) error
}

// setListData stores items in a list response. An empty list leaves data
// unset, as Prism Central does: the SDK resolves an empty JSON array to the
// first candidate element type (a projection), which prism-go-client then
// rejects as an unexpected type.
func setListData[T any](resp dataSetter, items []T) error {
	if len(items) == 0 {
		return nil
	}
	return resp.SetData(items)
}
