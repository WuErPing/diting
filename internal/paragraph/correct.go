package paragraph

import "strings"

// ApplyCorrections adopts model corrections only when they stay close to the
// original: the language model must fix ASR mistakes, not rewrite speech, so
// any candidate beyond a small edit budget is rejected.
func ApplyCorrections(originals, corrected []string) ([]string, int) {
	result := append([]string(nil), originals...)
	if len(corrected) != len(originals) {
		return result, 0
	}
	applied := 0
	for i, original := range originals {
		if original == "" {
			continue
		}
		candidate := strings.TrimSpace(corrected[i])
		if candidate == "" || candidate == original {
			continue
		}
		budget := correctionBudget(original)
		if editDistanceWithin(original, candidate, budget) > budget {
			continue
		}
		result[i] = candidate
		applied++
	}
	return result, applied
}

// correctionBudget allows one edit plus one more per ten characters.
func correctionBudget(text string) int {
	return 1 + len([]rune(text))/10
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
