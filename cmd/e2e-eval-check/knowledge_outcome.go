package main

// outcomesFromKnowledge converts knowledge case results into the unified
// caseOutcome shape, keeping the RAG ordering fields.
func outcomesFromKnowledge(results []knowledgeCaseResult) []caseOutcome {
	out := make([]caseOutcome, 0, len(results))
	for _, r := range results {
		noAnswerPass := r.NoAnswerPass
		citeCorrect := r.CitationCorrect
		out = append(out, caseOutcome{
			CaseID:          r.CaseID,
			Passed:          casePassed(r),
			AssertionMode:   "retrieval",
			Recall:          r.Recall,
			MRR:             r.MRR,
			NDCG:            r.NDCG,
			RetrievedCount:  r.RetrievedCount,
			RetrievedDocIDs: r.RetrievedDocumentIDs,
			NoAnswerPass:    &noAnswerPass,
			CitationCorrect: &citeCorrect,
		})
	}
	return out
}

// CitationAnnotated reports whether the case declared citation documents.
func (r knowledgeCaseResult) CitationAnnotated() bool { return len(r.CitationDocuments) > 0 }

// aggregateFromKnowledge rolls up knowledge case results per the phase-1
// aggregation contract: ordering metrics averaged over non-no-answer cases,
// no-answer pass rate over expect-no-answer cases, citation pass rate over
// annotated cases, and the unified pass_rate from per-case outcomes.
func aggregateFromKnowledge(results []knowledgeCaseResult) aggregate {
	agg := aggregate{CaseCount: len(results)}
	var recallSum, mrrSum, ndcgSum, relevantSum, passed float64
	for _, r := range results {
		passed += boolToFloat(casePassed(r))
		if !r.ExpectNoAnswer {
			recallSum += r.Recall
			mrrSum += r.MRR
			ndcgSum += r.NDCG
			relevantSum += boolToFloat(r.Relevant)
			agg.NonNoAnswerCount++
		} else {
			agg.NoAnswerCount++
			agg.NoAnswerPassRate += boolToFloat(r.NoAnswerPass)
		}
		if r.CitationAnnotated() && r.CitationCorrect {
			agg.CitationPassRate++
		}
	}
	n := agg.NonNoAnswerCount
	if n > 0 {
		agg.Recall = recallSum / float64(n)
		agg.MRR = mrrSum / float64(n)
		agg.NDCG = ndcgSum / float64(n)
		agg.RelevantRate = relevantSum / float64(n)
	}
	if agg.NoAnswerCount > 0 {
		agg.NoAnswerPassRate /= float64(agg.NoAnswerCount)
	}
	if annotated := citationAnnotatedCount(results); annotated > 0 {
		agg.CitationPassRate /= float64(annotated)
	}
	if len(results) > 0 {
		agg.PassRate = passed / float64(len(results))
	}
	return agg
}

// boolToFloat converts a per-case boolean counter to 1.0/0.0 so case flags can
// be summed without branching (keeps aggregateFromKnowledge under the
// cyclomatic gate).
func boolToFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// casePassed is the shared per-case pass semantic: no-answer must pass and,
// when the case declares citation documents, they must all be retrieved.
func casePassed(r knowledgeCaseResult) bool {
	return r.NoAnswerPass && (!r.CitationAnnotated() || r.CitationCorrect)
}
