package httpapi

import (
	"errors"
	"foundry/internal/knowledge"
	"foundry/internal/store"
	"net/http"
	"net/url"
	"strconv"
)

func (s *server) knowledgeRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && r.URL.Path == "/api/knowledge/schema" {
		writeJSON(w, http.StatusOK, knowledge.Schema())
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/api/knowledge" {
		q, err := parseKnowledgeQuery(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_knowledge_query", "knowledge query is invalid")
			return
		}
		if !s.recoveryReadOnly {
			if err = s.store.SyncKnowledge(r.Context(), false); err != nil {
				writeKnowledgeError(w, err)
				return
			}
		}
		graph, err := s.store.QueryKnowledge(r.Context(), q)
		if err != nil {
			writeKnowledgeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, graph)
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == "/api/knowledge/rebuild" {
		var request *struct{}
		if err := decodeJSON(w, r, &request); err != nil {
			writeDecodeError(w, err)
			return
		}
		if request == nil {
			writeError(w, http.StatusBadRequest, "invalid_knowledge_query", "rebuild requires an empty JSON object")
			return
		}
		if err := s.store.SyncKnowledge(r.Context(), true); err != nil {
			writeKnowledgeError(w, err)
			return
		}
		graph, err := s.store.QueryKnowledge(r.Context(), knowledge.Query{})
		if err != nil {
			writeKnowledgeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, graph)
		return
	}
	writeError(w, http.StatusNotFound, "not_found", "route not found")
}
func writeKnowledgeError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrKnowledgeQuery) {
		writeError(w, http.StatusBadRequest, "invalid_knowledge_query", "knowledge query is invalid")
		return
	}
	writeError(w, http.StatusServiceUnavailable, "knowledge_unavailable", "knowledge graph is unavailable or invalid; rebuild derived graph")
}
func parseKnowledgeQuery(r *http.Request) (knowledge.Query, error) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return knowledge.Query{}, store.ErrKnowledgeQuery
	}
	q := knowledge.Query{}
	for key, v := range values {
		if len(v) != 1 {
			return q, store.ErrKnowledgeQuery
		}
		switch key {
		case "project_id":
			q.ProjectID = v[0]
		case "mission_id":
			q.MissionID = v[0]
		case "kind":
			q.Kind = v[0]
		case "q":
			q.Search = v[0]
		case "root":
			q.Root = v[0]
		case "mode":
			q.Mode = v[0]
		case "depth":
			q.Depth, err = strconv.Atoi(v[0])
			if err != nil || q.Depth < 1 || q.Depth > 4 {
				return q, store.ErrKnowledgeQuery
			}
		case "limit":
			q.Limit, err = strconv.Atoi(v[0])
			if err != nil || q.Limit < 1 || q.Limit > 200 {
				return q, store.ErrKnowledgeQuery
			}
		default:
			return q, store.ErrKnowledgeQuery
		}
	}
	if q.Mode != "" && q.Mode != "neighbors" && q.Mode != "impact" && q.Mode != "lineage" {
		return q, store.ErrKnowledgeQuery
	}
	return q, nil
}
