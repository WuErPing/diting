package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"diting/internal/paragraph"
)

type dayMinutesSettings interface {
	SummarizeDay(ctx context.Context, date string) (paragraph.DayMinutesResult, error)
}

func (s *Server) handleSummarizeDay(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "ORIGIN_REJECTED", "请求来源无效")
		return
	}
	date := r.PathValue("date")
	parsed, err := time.ParseInLocation("2006-01-02", date, time.Local)
	if err != nil || parsed.Format("2006-01-02") != date {
		writeError(w, http.StatusBadRequest, "INVALID_DATE", "日期必须是 YYYY-MM-DD")
		return
	}
	settings, ok := s.settings.(dayMinutesSettings)
	if s.settings == nil || !ok {
		writeError(w, http.StatusServiceUnavailable, "DAY_SUMMARY_UNAVAILABLE", "按天会议纪要不可用")
		return
	}
	result, err := settings.SummarizeDay(r.Context(), date)
	if errors.Is(err, paragraph.ErrDayNotFound) {
		writeError(w, http.StatusNotFound, "DAY_NOT_FOUND", "当天没有可总结的转写")
		return
	}
	if errors.Is(err, paragraph.ErrDaySummaryUnavailable) {
		writeError(w, http.StatusServiceUnavailable, "DAY_SUMMARY_UNAVAILABLE", "按天会议纪要不可用")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "DAY_SUMMARY_FAILED", "会议纪要生成失败，请检查模型 API 地址、凭证、模型 ID 和网络")
		return
	}
	writeJSON(w, http.StatusOK, result)
}
