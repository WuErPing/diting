package engine

import (
	"strings"
	"unicode"
)

func reconcileTranscript(offline, online string, secondary ...string) string {
	offline = strings.TrimSpace(offline)
	online = strings.TrimSpace(online)
	if offline == "" || online == "" {
		return offline
	}
	offlineContent, suffix := terminalPunctuation(offline)
	if offlineContent == "" || hasPunctuation(online) || !containsHan(offlineContent) || !containsHan(online) {
		return offline
	}
	offlineComparable := compactText(offlineContent)
	onlineComparable := compactText(online)
	if offlineComparable == onlineComparable {
		return offline
	}
	if hasPunctuation(offlineContent) || len([]rune(offlineComparable)) > 30 {
		return offline
	}
	if len(secondary) > 0 {
		alternative := strings.TrimSpace(secondary[0])
		if alternative != "" && !hasPunctuation(alternative) && containsHan(alternative) &&
			onlineComparable == compactText(alternative) && editDistanceWithin(offlineComparable, onlineComparable, 2) <= 2 &&
			!droppedLeadingText(offlineComparable, onlineComparable) {
			return online + suffix
		}
	}
	if editDistanceWithin(offlineComparable, onlineComparable, 1) == 1 && !droppedLeadingText(offlineComparable, onlineComparable) {
		return online + suffix
	}
	return offline
}

// droppedLeadingText reports whether online is offline with leading characters
// removed. Streaming recognizers often miss the first syllables before their
// encoder warms up, so offline wins in that case even when streamings agree.
func droppedLeadingText(offline, online string) bool {
	offlineRunes := []rune(offline)
	onlineRunes := []rune(online)
	return len(onlineRunes) > 0 && len(onlineRunes) < len(offlineRunes) &&
		string(offlineRunes[len(offlineRunes)-len(onlineRunes):]) == online
}

func terminalPunctuation(text string) (string, string) {
	runes := []rune(text)
	index := len(runes)
	for index > 0 && (unicode.IsPunct(runes[index-1]) || unicode.IsSpace(runes[index-1])) {
		index--
	}
	return strings.TrimSpace(string(runes[:index])), string(runes[index:])
}

func compactText(text string) string {
	return strings.Map(func(character rune) rune {
		if unicode.IsSpace(character) {
			return -1
		}
		return character
	}, text)
}

func hasPunctuation(text string) bool {
	for _, character := range text {
		if unicode.IsPunct(character) {
			return true
		}
	}
	return false
}

func containsHan(text string) bool {
	for _, character := range text {
		if unicode.Is(unicode.Han, character) {
			return true
		}
	}
	return false
}

func editDistanceWithin(a, b string, limit int) int {
	left, right := []rune(a), []rune(b)
	if len(left) > len(right) {
		left, right = right, left
	}
	if len(right)-len(left) > limit {
		return limit + 1
	}
	previous := make([]int, len(left)+1)
	for i := range previous {
		previous[i] = i
	}
	for row, rightCharacter := range right {
		current := make([]int, len(left)+1)
		current[0] = row + 1
		best := current[0]
		for column, leftCharacter := range left {
			cost := 1
			if leftCharacter == rightCharacter {
				cost = 0
			}
			current[column+1] = min(current[column]+1, previous[column+1]+1, previous[column]+cost)
			best = min(best, current[column+1])
		}
		if best > limit {
			return limit + 1
		}
		previous = current
	}
	return previous[len(left)]
}
