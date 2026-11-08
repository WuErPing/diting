package speaker

const windowAgreementThreshold = float32(0.45)

func AggregateEmbeddings(embeddings [][]float32, weights []float32) ([]float32, float32) {
	normalized := make([][]float32, len(embeddings))
	effectiveWeights := make([]float32, len(embeddings))
	var totalWeight float32
	for i, embedding := range embeddings {
		normalized[i] = Normalize(embedding)
		if normalized[i] == nil {
			continue
		}
		weight := float32(1)
		if i < len(weights) {
			weight = weights[i]
		}
		if weight <= 0 {
			continue
		}
		effectiveWeights[i] = weight
		totalWeight += weight
	}
	if totalWeight == 0 {
		return nil, 0
	}

	best, bestSupport, bestSimilarity := -1, float32(-1), float32(-1)
	for i := range normalized {
		if normalized[i] == nil || effectiveWeights[i] == 0 {
			continue
		}
		var support, similarity float32
		for j := range normalized {
			if normalized[j] == nil || effectiveWeights[j] == 0 {
				continue
			}
			score := Cosine(normalized[i], normalized[j])
			if score >= windowAgreementThreshold {
				support += effectiveWeights[j]
				similarity += score * effectiveWeights[j]
			}
		}
		if support > bestSupport || support == bestSupport && similarity > bestSimilarity {
			best, bestSupport, bestSimilarity = i, support, similarity
		}
	}

	sum := make([]float32, len(normalized[best]))
	for i, embedding := range normalized {
		if embedding == nil || effectiveWeights[i] == 0 || Cosine(normalized[best], embedding) < windowAgreementThreshold {
			continue
		}
		for dimension, value := range embedding {
			sum[dimension] += value * effectiveWeights[i]
		}
	}
	return Normalize(sum), bestSupport / totalWeight
}
