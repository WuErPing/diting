package server

import (
	"log"
	"net/http"
	"sort"
	"time"

	"diting/internal/store"
)

type dayView struct {
	store.DayStat
	Minutes bool `json:"minutes"`
}

type daysResponse struct {
	Days []dayView `json:"days"`
}

func (s *Server) handleDays(w http.ResponseWriter, _ *http.Request) {
	days, err := s.store.Days()
	if err != nil {
		log.Printf("Failed to read daily archive: %v", err)
		writeError(w, http.StatusInternalServerError, "DAYS_FAILED", "无法读取按天归档")
		return
	}
	minutesDates, err := s.store.MinutesDates()
	if err != nil {
		log.Printf("Failed to list saved meeting minutes: %v", err)
		writeError(w, http.StatusInternalServerError, "DAYS_FAILED", "无法读取按天归档")
		return
	}
	withMinutes := make(map[string]bool, len(minutesDates))
	for _, date := range minutesDates {
		withMinutes[date] = true
	}
	views := make([]dayView, 0, len(days)+len(minutesDates))
	seen := make(map[string]bool, len(days))
	for _, day := range days {
		views = append(views, dayView{DayStat: day, Minutes: withMinutes[day.Date]})
		seen[day.Date] = true
	}
	// Keep days whose transcripts were deleted but whose minutes remain.
	for _, date := range minutesDates {
		if !seen[date] {
			views = append(views, dayView{DayStat: store.DayStat{Date: date}, Minutes: true})
		}
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Date < views[j].Date })
	writeJSON(w, http.StatusOK, daysResponse{Days: views})
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

	dryRun := r.URL.Query().Get("dryRun") == "1" || r.URL.Query().Get("dryRun") == "true"
	result, deleteErr := s.store.DeleteDay(date, dryRun)
	if deleteErr != nil {
		log.Printf("Failed to delete data for %s: %v", date, deleteErr)
		writeError(w, http.StatusInternalServerError, "DAY_DELETE_FAILED", "删除当天数据失败")
		return
	}
	if dryRun {
		writeJSON(w, http.StatusOK, deleteDayResponse{DayDelete: result})
		return
	}
	removedParagraphs, cleanupErr := s.finishDayDeletion(result)
	if cleanupErr != nil {
		log.Printf("Failed to clean up derived paragraphs for %s: %v", date, cleanupErr)
		writeError(w, http.StatusInternalServerError, "DAY_DELETE_FAILED", "删除当天数据失败")
		return
	}
	if err := s.store.DeleteMinutes(date); err != nil {
		log.Printf("Failed to delete meeting minutes for %s: %v", date, err)
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
