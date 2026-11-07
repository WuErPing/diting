package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"diting/internal/lexicon"
)

func (s *Server) handleLexicon(w http.ResponseWriter, _ *http.Request) {
	rules := []lexicon.Rule{}
	if s.lexicon != nil {
		if stored := s.lexicon.Rules(); stored != nil {
			rules = stored
		}
	}
	writeJSON(w, http.StatusOK, struct {
		Rules []lexicon.Rule `json:"rules"`
	}{Rules: rules})
}

func (s *Server) handleLexiconUpdate(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "ORIGIN_REJECTED", "请求来源无效")
		return
	}
	if mediaType := r.Header.Get("Content-Type"); !isJSON(mediaType) {
		writeError(w, http.StatusUnsupportedMediaType, "CONTENT_TYPE_REQUIRED", "请使用 application/json")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var input struct {
		Rules []lexicon.Rule `json:"rules"`
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
	if s.lexicon == nil {
		writeError(w, http.StatusServiceUnavailable, "LEXICON_UNAVAILABLE", "术语表未启用")
		return
	}
	if err := s.lexicon.Replace(input.Rules); err != nil {
		if errors.Is(err, lexicon.ErrInvalidRule) {
			writeError(w, http.StatusBadRequest, "INVALID_LEXICON", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "LEXICON_SAVE_FAILED", "保存术语表失败")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Rules []lexicon.Rule `json:"rules"`
	}{Rules: s.lexicon.Rules()})
}
