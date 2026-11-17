package server

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"strings"

	"diting/internal/store"
)

type deleteSegmentResponse struct {
	store.SegmentDelete
	RemovedParagraphs int `json:"removedParagraphs"`
}

func (s *Server) handleDeleteSegment(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "ORIGIN_REJECTED", "请求来源无效")
		return
	}
	id := r.PathValue("id")
	if !segmentIDPattern.MatchString(id) {
		writeError(w, http.StatusBadRequest, "INVALID_SEGMENT_ID", "录音 ID 无效")
		return
	}
	result, err := s.store.DeleteSegment(id)
	if errors.Is(err, fs.ErrNotExist) {
		writeError(w, http.StatusNotFound, "SEGMENT_NOT_FOUND", "录音不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SEGMENT_DELETE_FAILED", "删除记录失败")
		return
	}

	removedParagraphs := 0
	if s.paragraphs != nil {
		removedParagraphs, err = s.paragraphs.RemoveSegments(map[string]struct{}{id: {}})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "SEGMENT_DELETE_FAILED", "删除记录失败")
			return
		}
	}
	if s.onArchiveChange != nil {
		s.onArchiveChange()
	}
	s.invalidateStats()
	if s.hub != nil {
		s.hub.publish(sseEvent{Type: "archive", Data: map[string]string{"id": id}})
	}
	writeJSON(w, http.StatusOK, deleteSegmentResponse{SegmentDelete: result, RemovedParagraphs: removedParagraphs})
}

type deleteSegmentsResponse struct {
	Data              []store.SegmentDelete `json:"data"`
	RemovedParagraphs int                   `json:"removedParagraphs"`
}

func (s *Server) handleDeleteSegments(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "ORIGIN_REJECTED", "请求来源无效")
		return
	}
	if mediaType := r.Header.Get("Content-Type"); !strings.HasPrefix(mediaType, "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "CONTENT_TYPE_REQUIRED", "请使用 application/json")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	var input struct {
		IDs []string `json:"ids"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "请求内容不是有效 JSON")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "请求只能包含一个 JSON 对象")
		return
	}
	if len(input.IDs) == 0 || len(input.IDs) > 100 {
		writeError(w, http.StatusBadRequest, "INVALID_SEGMENT_ID", "ids 必须包含 1 到 100 个录音 ID")
		return
	}
	seen := make(map[string]struct{}, len(input.IDs))
	ids := make([]string, 0, len(input.IDs))
	for _, id := range input.IDs {
		if !segmentIDPattern.MatchString(id) {
			writeError(w, http.StatusBadRequest, "INVALID_SEGMENT_ID", "录音 ID 无效")
			return
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}

	results, err := s.store.DeleteSegments(ids)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SEGMENT_DELETE_FAILED", "删除记录失败")
		return
	}
	removedParagraphs := 0
	if s.paragraphs != nil && len(results) > 0 {
		removed := make(map[string]struct{}, len(results))
		for _, result := range results {
			removed[result.ID] = struct{}{}
		}
		removedParagraphs, err = s.paragraphs.RemoveSegments(removed)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "SEGMENT_DELETE_FAILED", "删除记录失败")
			return
		}
	}
	if len(results) > 0 {
		if s.onArchiveChange != nil {
			s.onArchiveChange()
		}
		s.invalidateStats()
		if s.hub != nil {
			s.hub.publish(sseEvent{Type: "archive", Data: map[string]int{"count": len(results)}})
		}
	}
	writeJSON(w, http.StatusOK, deleteSegmentsResponse{Data: results, RemovedParagraphs: removedParagraphs})
}
