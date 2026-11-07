package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"diting/internal/speaker"
)

func (s *Server) handleSpeakers(w http.ResponseWriter, _ *http.Request) {
	views := []speaker.View{}
	if s.speakers != nil {
		if stored := s.speakers.Speakers(); stored != nil {
			views = stored
		}
	}
	writeJSON(w, http.StatusOK, struct {
		Data []speaker.View `json:"data"`
	}{Data: views})
}

func (s *Server) handleSpeakerAlias(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "ORIGIN_REJECTED", "请求来源无效")
		return
	}
	if mediaType := r.Header.Get("Content-Type"); !isJSON(mediaType) {
		writeError(w, http.StatusUnsupportedMediaType, "CONTENT_TYPE_REQUIRED", "请使用 application/json")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var input struct {
		Alias string `json:"alias"`
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

	label := r.PathValue("label")
	if !speakerLabelPattern.MatchString(label) {
		writeError(w, http.StatusBadRequest, "INVALID_SPEAKER", "说话人标签无效")
		return
	}
	if s.speakers == nil {
		writeError(w, http.StatusServiceUnavailable, "SPEAKER_UNAVAILABLE", "说话人识别未启用")
		return
	}
	if err := s.speakers.SetSpeakerAlias(label, input.Alias); err != nil {
		switch {
		case errors.Is(err, speaker.ErrUnknownSpeaker):
			writeError(w, http.StatusNotFound, "SPEAKER_NOT_FOUND", "说话人不存在")
		case errors.Is(err, speaker.ErrInvalidAlias):
			writeError(w, http.StatusBadRequest, "INVALID_ALIAS", err.Error())
		case errors.Is(err, speaker.ErrMergedSpeaker):
			writeError(w, http.StatusConflict, "SPEAKER_MERGED", err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "SPEAKER_SAVE_FAILED", "保存人名失败")
		}
		return
	}

	if s.onArchiveChange != nil {
		s.onArchiveChange()
	}
	if s.hub != nil {
		s.hub.publish(sseEvent{Type: "archive", Data: map[string]string{"action": "speaker-alias"}})
	}

	for _, view := range s.speakers.Speakers() {
		if view.Label == label {
			writeJSON(w, http.StatusOK, view)
			return
		}
	}
	writeJSON(w, http.StatusOK, speaker.View{Label: label})
}

func (s *Server) handleSpeakerMerge(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "ORIGIN_REJECTED", "请求来源无效")
		return
	}
	if mediaType := r.Header.Get("Content-Type"); !isJSON(mediaType) {
		writeError(w, http.StatusUnsupportedMediaType, "CONTENT_TYPE_REQUIRED", "请使用 application/json")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var input struct {
		Target string `json:"target"`
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

	label := r.PathValue("label")
	if !speakerLabelPattern.MatchString(label) {
		writeError(w, http.StatusBadRequest, "INVALID_SPEAKER", "说话人标签无效")
		return
	}
	if input.Target != "" && !speakerLabelPattern.MatchString(input.Target) {
		writeError(w, http.StatusBadRequest, "INVALID_SPEAKER", "目标说话人标签无效")
		return
	}
	if s.speakers == nil {
		writeError(w, http.StatusServiceUnavailable, "SPEAKER_UNAVAILABLE", "说话人识别未启用")
		return
	}
	if err := s.speakers.SetSpeakerMerge(label, input.Target); err != nil {
		switch {
		case errors.Is(err, speaker.ErrUnknownSpeaker):
			writeError(w, http.StatusNotFound, "SPEAKER_NOT_FOUND", "说话人不存在")
		case errors.Is(err, speaker.ErrInvalidMerge):
			writeError(w, http.StatusBadRequest, "INVALID_MERGE", err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "SPEAKER_SAVE_FAILED", "保存合并失败")
		}
		return
	}

	if s.onArchiveChange != nil {
		s.onArchiveChange()
	}
	if s.hub != nil {
		s.hub.publish(sseEvent{Type: "archive", Data: map[string]string{"action": "speaker-merge"}})
	}

	for _, view := range s.speakers.Speakers() {
		if view.Label == label {
			writeJSON(w, http.StatusOK, view)
			return
		}
	}
	writeJSON(w, http.StatusOK, speaker.View{Label: label})
}

func isJSON(mediaType string) bool {
	return len(mediaType) >= len("application/json") && mediaType[:len("application/json")] == "application/json"
}
