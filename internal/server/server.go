package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"diting/internal/engine"
	"diting/internal/paragraph"
	"diting/internal/store"
)

var segmentIDPattern = regexp.MustCompile(`^\d{8}-\d{6}\.\d{9}$`)

type Server struct {
	engine     *engine.Engine
	store      *store.Store
	paragraphs *paragraph.Store
	merge      bool
	hub        *eventHub
	mux        *http.ServeMux
}

func New(ctx context.Context, recognizer *engine.Engine, segmentStore *store.Store,
	paragraphs *paragraph.Store, merge bool, assets fs.FS) *Server {
	s := &Server{
		engine:     recognizer,
		store:      segmentStore,
		paragraphs: paragraphs,
		merge:      merge,
		hub:        newEventHub(ctx, recognizer.Events()),
		mux:        http.NewServeMux(),
	}
	s.mux.HandleFunc("GET /api/status", s.handleStatus)
	s.mux.HandleFunc("PUT /api/capture", s.handleCapture)
	s.mux.HandleFunc("GET /api/segments", s.handleSegments)
	s.mux.HandleFunc("GET /api/segments/{id}/audio", s.handleAudio)
	s.mux.HandleFunc("GET /api/markdown", s.handleMarkdown)
	s.mux.HandleFunc("GET /api/events", s.handleEvents)
	if assets != nil {
		s.mux.Handle("GET /", http.FileServer(http.FS(assets)))
	}
	return s
}

func (s *Server) Handler() http.Handler {
	return s.securityHeaders(s.hostGuard(s.mux))
}

func (s *Server) Close() {
	s.hub.Close()
}

type paragraphEvent struct {
	ID           string    `json:"id"`
	StartedAt    time.Time `json:"startedAt"`
	EndedAt      time.Time `json:"endedAt"`
	Speaker      string    `json:"speaker,omitempty"`
	SegmentCount int       `json:"segmentCount"`
	Source       string    `json:"source"`
}

func (s *Server) PublishParagraph(p paragraph.Paragraph) {
	if s.hub == nil {
		return
	}
	s.hub.publish(sseEvent{Type: "paragraph", Data: paragraphEvent{
		ID:           p.ID,
		StartedAt:    p.StartedAt,
		EndedAt:      p.EndedAt,
		Speaker:      p.Speaker,
		SegmentCount: p.SegmentCount(),
		Source:       p.Source,
	}})
}

type statusResponse struct {
	engine.Status
	UptimeSeconds int64 `json:"uptimeSeconds"`
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.currentStatus())
}

func (s *Server) currentStatus() statusResponse {
	status := s.engine.Status()
	response := statusResponse{Status: status}
	if status.StartedAt != nil {
		response.UptimeSeconds = int64(time.Since(*status.StartedAt).Seconds())
	}
	return response
}

func (s *Server) handleCapture(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "ORIGIN_REJECTED", "请求来源无效")
		return
	}
	if mediaType := r.Header.Get("Content-Type"); !strings.HasPrefix(mediaType, "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "CONTENT_TYPE_REQUIRED", "请使用 application/json")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var input struct {
		State engine.CaptureState `json:"state"`
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

	switch input.State {
	case engine.StateRunning:
		if err := s.engine.Start(); err != nil {
			writeError(w, http.StatusInternalServerError, "CAPTURE_START_FAILED", "无法启动麦克风，请检查权限和输入设备")
			return
		}
	case engine.StateStopped:
		s.engine.Stop()
	default:
		writeError(w, http.StatusBadRequest, "INVALID_STATE", "state 只能是 RUNNING 或 STOPPED")
		return
	}
	writeJSON(w, http.StatusOK, s.currentStatus())
}

func (s *Server) handleSegments(w http.ResponseWriter, r *http.Request) {
	limit := 50
	var err error
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			writeError(w, http.StatusBadRequest, "INVALID_LIMIT", "limit 必须在 1 到 100 之间")
			return
		}
	}
	var before time.Time
	if raw := r.URL.Query().Get("before"); raw != "" {
		before, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_BEFORE", "before 必须是 RFC3339 时间")
			return
		}
	}
	segments := s.store.List(limit, before)
	response := struct {
		Data       []store.Segment `json:"data"`
		NextBefore string          `json:"nextBefore,omitempty"`
	}{Data: segments}
	if len(segments) == limit {
		response.NextBefore = segments[len(segments)-1].StartedAt.Format(time.RFC3339Nano)
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleAudio(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !segmentIDPattern.MatchString(id) {
		writeError(w, http.StatusBadRequest, "INVALID_SEGMENT_ID", "录音 ID 无效")
		return
	}
	path, err := s.store.Audio(id)
	if errors.Is(err, fs.ErrNotExist) {
		writeError(w, http.StatusNotFound, "SEGMENT_NOT_FOUND", "录音不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "AUDIO_READ_FAILED", "无法读取录音")
		return
	}
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", id+".wav"))
	http.ServeFile(w, r, path)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "STREAM_UNSUPPORTED", "当前连接不支持实时事件")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	events, unsubscribe := s.hub.subscribe()
	defer unsubscribe()
	if err := writeSSE(w, "status", s.currentStatus()); err != nil {
		return
	}
	flusher.Flush()

	keepAlive := time.NewTicker(15 * time.Second)
	defer keepAlive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepAlive.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case event, ok := <-events:
			if !ok {
				return
			}
			if err := writeSSE(w, event.Type, event.Data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeSSE(w io.Writer, name string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, payload)
	return err
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; media-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) hostGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if parsed, _, err := net.SplitHostPort(host); err == nil {
			host = parsed
		}
		host = strings.Trim(strings.ToLower(host), "[]")
		if host != "127.0.0.1" && host != "localhost" && host != "::1" {
			writeError(w, http.StatusForbidden, "HOST_REJECTED", "只允许本机访问")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func sameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Scheme == "http" && parsed.Host == r.Host
}
