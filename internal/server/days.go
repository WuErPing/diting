package server

import (
	"errors"
	"log"
	"net/http"
	"time"

	"diting/internal/store"
)

type daysResponse struct {
	Days []store.DayStat `json:"days"`
}

func (s *Server) handleDays(w http.ResponseWriter, _ *http.Request) {
	days, err := s.store.Days()
	if err != nil {
		log.Printf("读取按天归档失败: %v", err)
		writeError(w, http.StatusInternalServerError, "DAYS_FAILED", "无法读取按天归档")
		return
	}
	if days == nil {
		days = []store.DayStat{}
	}
	writeJSON(w, http.StatusOK, daysResponse{Days: days})
}

type deleteDayResponse struct {
	store.DayDelete
	RemovedParagraphs int `json:"removedParagraphs"`
}

func (s *Server) handleDeleteDay(w http.ResponseWriter, r *http.Request) {
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

	result, deleteErr := s.store.DeleteDay(date)
	removedParagraphs, cleanupErr := s.finishDayDeletion(result)
	if err := errors.Join(deleteErr, cleanupErr); err != nil {
		log.Printf("删除 %s 的数据失败: %v", date, err)
		writeError(w, http.StatusInternalServerError, "DAY_DELETE_FAILED", "删除当天数据失败")
		return
	}

	if s.hub != nil {
		s.hub.publish(sseEvent{Type: "archive", Data: map[string]string{"date": date}})
	}
	writeJSON(w, http.StatusOK, deleteDayResponse{DayDelete: result, RemovedParagraphs: removedParagraphs})
}

func (s *Server) finishDayDeletion(result store.DayDelete) (int, error) {
	removed := 0
	if s.paragraphs != nil && len(result.SegmentIDs) > 0 {
		ids := make(map[string]struct{}, len(result.SegmentIDs))
		for _, id := range result.SegmentIDs {
			ids[id] = struct{}{}
		}
		count, err := s.paragraphs.RemoveSegments(ids)
		if err != nil {
			return removed, err
		}
		removed = count
	}
	if s.onArchiveChange != nil {
		s.onArchiveChange()
	}
	s.invalidateStats()
	return removed, nil
}
