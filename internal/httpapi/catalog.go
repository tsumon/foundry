package httpapi

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"foundry/internal/catalog"
	"foundry/internal/domain"
	"foundry/internal/store"
)

func (s *server) catalogRoute(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/catalog":
		snapshot, err := s.catalog.Snapshot()
		if err != nil {
			writeCatalogError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"catalog": snapshot})
	case r.Method == http.MethodPost && r.URL.Path == "/api/catalog/commands":
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeError(w, http.StatusBadRequest, "invalid_catalog", "catalog command is invalid")
			} else {
				writeError(w, http.StatusInternalServerError, "internal", "catalog operation failed")
			}
			return
		}
		command, err := catalog.DecodeCommand(raw)
		if err != nil {
			writeCatalogError(w, err)
			return
		}
		snapshot, err := s.catalog.Execute(command)
		if err != nil {
			writeCatalogError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"catalog": snapshot})
	case r.Method == http.MethodGet && r.URL.Path == "/api/catalog/events":
		after, limit, err := catalogPagination(r.URL.RawQuery)
		if err != nil {
			writeCatalogError(w, err)
			return
		}
		events, err := s.catalog.Events(after, limit)
		if err != nil {
			writeCatalogError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"events": events})
	default:
		writeError(w, http.StatusNotFound, "not_found", "route not found")
	}
}

func catalogPagination(rawQuery string) (int64, int, error) {
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return 0, 0, invalidCatalogQuery()
	}
	for key, values := range query {
		if key != "after" && key != "limit" || len(values) != 1 || !catalogDigits(values[0]) {
			return 0, 0, invalidCatalogQuery()
		}
	}
	after := int64(0)
	if values, ok := query["after"]; ok {
		after, err = strconv.ParseInt(values[0], 10, 64)
		if err != nil {
			return 0, 0, invalidCatalogQuery()
		}
	}
	limit := int64(100)
	if values, ok := query["limit"]; ok {
		limit, err = strconv.ParseInt(values[0], 10, 64)
		if err != nil || limit < 1 || limit > 100 {
			return 0, 0, invalidCatalogQuery()
		}
	}
	return after, int(limit), nil
}

func catalogDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

func invalidCatalogQuery() error {
	return domain.ErrInvalidCatalog
}

func writeCatalogError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrCatalogCorrupt):
		writeError(w, http.StatusInternalServerError, "catalog_corrupt", "catalog data is unavailable")
	case errors.Is(err, catalog.ErrUnknownCommand), errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "catalog resource or command was not found")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", "catalog revision or relationship conflicts with current data")
	case errors.Is(err, domain.ErrInvalidCatalog):
		writeError(w, http.StatusBadRequest, "invalid_catalog", "catalog request is invalid")
	default:
		writeError(w, http.StatusInternalServerError, "internal", "catalog operation failed")
	}
}
