package application

import (
	"math"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

// Retrieval metrics are pure functions over the ordered retrieved document
// IDs and the relevant document set. RetrievalEvaluation only carries ordered
// IDs without scores, so nDCG@k uses binary gain at each rank; graded gain
// requires scores from the retrieval side (future work).
//
// Edge semantics: empty relevant set or empty retrieved slice yields 0, never
// a division by zero; k is clamped to the retrieved slice length.

// RecallAtK is the fraction of relevant documents found in the top k results.
func RecallAtK(retrieved, relevant []string, k int) float64 {
	if len(relevant) == 0 || k <= 0 {
		return 0
	}
	if k > len(retrieved) {
		k = len(retrieved)
	}
	rel := documentSet(relevant)
	hits := 0
	for _, id := range retrieved[:k] {
		if rel[id] {
			hits++
		}
	}
	return float64(hits) / float64(len(relevant))
}

// PrecisionAtK is the fraction of the top k results that are relevant.
func PrecisionAtK(retrieved, relevant []string, k int) float64 {
	if k <= 0 {
		return 0
	}
	if k > len(retrieved) {
		k = len(retrieved)
	}
	if k == 0 {
		return 0
	}
	rel := documentSet(relevant)
	hits := 0
	for _, id := range retrieved[:k] {
		if rel[id] {
			hits++
		}
	}
	return float64(hits) / float64(k)
}

// MRR is the reciprocal of the rank of the first relevant result, 0 when no
// retrieved document is relevant.
func MRR(retrieved, relevant []string) float64 {
	rel := documentSet(relevant)
	for i, id := range retrieved {
		if rel[id] {
			return 1 / float64(i+1)
		}
	}
	return 0
}

// NDCGAtK normalizes DCG@k over the ideal ordering where all relevant
// documents are ranked first. Relevant set larger than k bounds IDCG at k.
func NDCGAtK(retrieved, relevant []string, k int) float64 {
	if k <= 0 {
		return 0
	}
	if k > len(retrieved) {
		k = len(retrieved)
	}
	if k == 0 {
		return 0
	}
	rel := documentSet(relevant)
	dcg := 0.0
	for i, id := range retrieved[:k] {
		if rel[id] {
			dcg += 1 / math.Log2(float64(i+2))
		}
	}
	ideal := min(len(relevant), k)
	if ideal == 0 {
		return 0
	}
	idcg := 0.0
	for i := range ideal {
		idcg += 1 / math.Log2(float64(i+2))
	}
	return dcg / idcg
}

// RetrievalK is the rank window used for per-case and aggregated retrieval
// metrics; it mirrors constants.DefaultRAGTopK so consumers never inline 5.
var RetrievalK = constants.DefaultRAGTopK

func documentSet(docs []string) map[string]bool {
	set := make(map[string]bool, len(docs))
	for _, d := range docs {
		set[d] = true
	}
	return set
}
