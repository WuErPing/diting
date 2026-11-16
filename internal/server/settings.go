package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"diting/internal/engine"
	"diting/internal/paragraph"
)

func (s *Server) handleSettings(w http.ResponseWriter, _ *http.Request) {
	if s.settings == nil {
		writeError(w, http.StatusServiceUnavailable, "SETTINGS_UNAVAILABLE", "段落合并未启用")
		return
	}
	snapshot := s.settings.Snapshot()
	writeJSON(w, http.StatusOK, struct {
		Available bool   `json:"available"`
		Model     string `json:"model"`
		Correct   bool   `json:"correct"`
		ASR       string `json:"asr"`
	}{Available: true, Model: snapshot.Model, Correct: snapshot.Correct, ASR: snapshot.ASR})
}

func (s *Server) handleSettingsUpdate(w http.ResponseWriter, r *http.Request) {
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
		Model   string `json:"model"`
		Correct bool   `json:"correct"`
		ASR     string `json:"asr"`
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
	if s.settings == nil {
		writeError(w, http.StatusServiceUnavailable, "SETTINGS_UNAVAILABLE", "段落合并未启用")
		return
	}
	if input.ASR == "" {
		input.ASR = s.settings.Snapshot().ASR
	}
	if err := s.settings.Update(input.Model, input.Correct, input.ASR); err != nil {
		if errors.Is(err, paragraph.ErrInvalidSettings) {
			writeError(w, http.StatusBadRequest, "INVALID_SETTINGS", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "SETTINGS_SAVE_FAILED", "保存设置失败")
		return
	}
	snapshot := s.settings.Snapshot()
	writeJSON(w, http.StatusOK, struct {
		Available bool   `json:"available"`
		Model     string `json:"model"`
		Correct   bool   `json:"correct"`
		ASR       string `json:"asr"`
	}{Available: true, Model: snapshot.Model, Correct: snapshot.Correct, ASR: snapshot.ASR})
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		writeError(w, http.StatusServiceUnavailable, "SETTINGS_UNAVAILABLE", "段落合并未启用")
		return
	}
	models, err := s.settings.AvailableModels(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "MODELS_UNAVAILABLE", "无法获取模型列表，请检查模型 API 地址、凭证和网络")
		return
	}
	if models == nil {
		models = []string{}
	}
	snapshot := s.settings.Snapshot()
	writeJSON(w, http.StatusOK, struct {
		Models  []string `json:"models"`
		Current string   `json:"current"`
	}{Models: models, Current: snapshot.Model})
}

func (s *Server) handleSplitParagraphs(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "ORIGIN_REJECTED", "请求来源无效")
		return
	}
	if mediaType := r.Header.Get("Content-Type"); !isJSON(mediaType) {
		writeError(w, http.StatusUnsupportedMediaType, "CONTENT_TYPE_REQUIRED", "请使用 application/json")
		return
	}
	if s.settings == nil {
		writeError(w, http.StatusServiceUnavailable, "SPLIT_UNAVAILABLE", "段落合并未启用")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	input := struct {
		Limit int `json:"limit"`
	}{Limit: 500}
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
	if input.Limit < 1 || input.Limit > maxMarkdownLimit {
		writeError(w, http.StatusBadRequest, "INVALID_LIMIT", "limit 必须在 1 到 5000 之间")
		return
	}
	result, err := s.settings.SplitRecent(r.Context(), input.Limit)
	if errors.Is(err, paragraph.ErrSplitUnavailable) {
		writeError(w, http.StatusServiceUnavailable, "SPLIT_UNAVAILABLE", "自动分段不可用")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "SPLIT_FAILED", "主题切分失败，请检查模型 API 地址、凭证、模型 ID 和网络")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleCorrectParagraphs(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "ORIGIN_REJECTED", "请求来源无效")
		return
	}
	if mediaType := r.Header.Get("Content-Type"); !isJSON(mediaType) {
		writeError(w, http.StatusUnsupportedMediaType, "CONTENT_TYPE_REQUIRED", "请使用 application/json")
		return
	}
	if s.settings == nil {
		writeError(w, http.StatusServiceUnavailable, "CORRECTION_UNAVAILABLE", "段落合并未启用")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	input := struct {
		Limit int `json:"limit"`
	}{Limit: 500}
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
	if input.Limit < 1 || input.Limit > maxMarkdownLimit {
		writeError(w, http.StatusBadRequest, "INVALID_LIMIT", "limit 必须在 1 到 5000 之间")
		return
	}
	result, err := s.settings.CorrectRecent(r.Context(), input.Limit)
	if errors.Is(err, paragraph.ErrCorrectionUnavailable) {
		writeError(w, http.StatusServiceUnavailable, "CORRECTION_UNAVAILABLE", "语义修正不可用")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "CORRECTION_FAILED", "语义修正失败，请检查模型 API 地址、凭证、模型 ID 和网络")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

type paragraphMetadata struct {
	ID        string    `json:"id"`
	StartedAt time.Time `json:"startedAt"`
	EndedAt   time.Time `json:"endedAt"`
	Title     string    `json:"title"`
	Summary   string    `json:"summary"`
	HasTopic  bool      `json:"hasTopic"`
}

func (s *Server) handleParagraphs(w http.ResponseWriter, r *http.Request) {
	limit := 500
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxMarkdownLimit {
			writeError(w, http.StatusBadRequest, "INVALID_LIMIT", "limit 必须在 1 到 5000 之间")
			return
		}
		limit = parsed
	}
	data := make([]paragraphMetadata, 0)
	if !s.merge || s.paragraphs == nil {
		writeJSON(w, http.StatusOK, struct {
			Data []paragraphMetadata `json:"data"`
		}{Data: data})
		return
	}
	segments, err := s.store.All()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "PARAGRAPHS_FAILED", "无法读取段落信息")
		return
	}
	selected := paragraph.RecentParagraphs(s.paragraphs.All(), segments, limit)
	for _, item := range selected {
		data = append(data, paragraphMetadata{
			ID: item.ID, StartedAt: item.StartedAt, EndedAt: item.EndedAt,
			Title: item.TopicTitle(), Summary: item.TopicSummary(), HasTopic: item.HasTopic(),
		})
	}
	writeJSON(w, http.StatusOK, struct {
		Data []paragraphMetadata `json:"data"`
	}{Data: data})
}

func (s *Server) handleRetranscribeParagraph(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "ORIGIN_REJECTED", "请求来源无效")
		return
	}
	id := r.PathValue("id")
	if !segmentIDPattern.MatchString(id) {
		writeError(w, http.StatusBadRequest, "INVALID_PARAGRAPH_ID", "段落 ID 无效")
		return
	}
	if s.settings == nil {
		writeError(w, http.StatusServiceUnavailable, "ASR_UNAVAILABLE", "ASR 重识别不可用")
		return
	}
	result, err := s.settings.RetranscribeParagraph(r.Context(), id)
	if errors.Is(err, paragraph.ErrParagraphNotFound) {
		writeError(w, http.StatusNotFound, "PARAGRAPH_NOT_FOUND", "段落不存在或已重新合并，请刷新文稿")
		return
	}
	if errors.Is(err, paragraph.ErrASRUnavailable) {
		writeError(w, http.StatusServiceUnavailable, "ASR_UNAVAILABLE", "ASR 重识别不可用")
		return
	}
	if errors.Is(err, engine.ErrAudioUnavailable) {
		writeError(w, http.StatusUnprocessableEntity, "AUDIO_UNAVAILABLE", "部分录音缺失或无法解码，整段文本未修改")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "ASR_FAILED", "ASR 重识别失败，请检查识别模型、凭证、网络和存储")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleSummarizeParagraph(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "ORIGIN_REJECTED", "请求来源无效")
		return
	}
	id := r.PathValue("id")
	if !segmentIDPattern.MatchString(id) {
		writeError(w, http.StatusBadRequest, "INVALID_PARAGRAPH_ID", "段落 ID 无效")
		return
	}
	if s.settings == nil {
		writeError(w, http.StatusServiceUnavailable, "SUMMARY_UNAVAILABLE", "段落总结不可用")
		return
	}
	result, err := s.settings.SummarizeParagraph(r.Context(), id)
	if errors.Is(err, paragraph.ErrParagraphNotFound) {
		writeError(w, http.StatusNotFound, "PARAGRAPH_NOT_FOUND", "段落不存在或已重新合并，请刷新文稿")
		return
	}
	if errors.Is(err, paragraph.ErrSummaryUnavailable) {
		writeError(w, http.StatusServiceUnavailable, "SUMMARY_UNAVAILABLE", "段落总结不可用")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "SUMMARY_FAILED", "段落总结失败，请检查模型 API 地址、凭证、模型 ID 和网络")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleCorrectParagraph(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "ORIGIN_REJECTED", "请求来源无效")
		return
	}
	id := r.PathValue("id")
	if !segmentIDPattern.MatchString(id) {
		writeError(w, http.StatusBadRequest, "INVALID_PARAGRAPH_ID", "段落 ID 无效")
		return
	}
	if s.settings == nil {
		writeError(w, http.StatusServiceUnavailable, "CORRECTION_UNAVAILABLE", "段落合并未启用")
		return
	}
	result, err := s.settings.CorrectParagraph(r.Context(), id)
	if errors.Is(err, paragraph.ErrParagraphNotFound) {
		writeError(w, http.StatusNotFound, "PARAGRAPH_NOT_FOUND", "段落不存在或已重新合并，请刷新文稿")
		return
	}
	if errors.Is(err, paragraph.ErrCorrectionUnavailable) {
		writeError(w, http.StatusServiceUnavailable, "CORRECTION_UNAVAILABLE", "语义修正不可用")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "CORRECTION_FAILED", "语义修正失败，请检查模型 API 地址、凭证、模型 ID 和网络")
		return
	}
	writeJSON(w, http.StatusOK, result)
}
