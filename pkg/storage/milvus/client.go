// Package milvus provides vector database integration via the Milvus SDK.
//
// This is a Phase 1 DDD-refactor relocation of pkg/vector. The old import
// path is retained as a re-export alias and will be removed in phase 5.

package milvus

import (
	"context"
	"fmt"
	"math"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/milvus-io/milvus-sdk-go/v2/client"
	"github.com/milvus-io/milvus-sdk-go/v2/entity"
	"go.uber.org/zap"
)

type VectorStore struct {
	mu       sync.RWMutex
	client   client.Client
	host     string
	port     string
	logger   *zap.Logger
	dim      int
	dimCache sync.Map // collectionName -> int
}

func NewVectorStore(host, port string, logger *zap.Logger) *VectorStore {
	return &VectorStore{
		host:   host,
		port:   port,
		logger: logger,
		dim:    1536,
	}
}

func (vs *VectorStore) doConnect(ctx context.Context) error {
	vs.logger.Info("connecting to Milvus", zap.String("host", vs.host), zap.String("port", vs.port))
	milvusAddr := fmt.Sprintf("%s:%s", vs.host, vs.port)

	dialer := &net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", milvusAddr)
	if err != nil {
		vs.logger.Warn("Milvus port not reachable", zap.Error(err))
		return fmt.Errorf("milvus port not reachable: %w", err)
	}
	conn.Close() //nolint:errcheck,gosec

	type result struct {
		client client.Client
		err    error
	}
	resultCh := make(chan result, 1)

	go func() {
		c, err := client.NewGrpcClient(ctx, milvusAddr)
		resultCh <- result{client: c, err: err}
	}()

	select {
	case res := <-resultCh:
		if res.err != nil {
			vs.logger.Error("failed to connect to Milvus", zap.Error(res.err))
			return fmt.Errorf("failed to connect to Milvus: %w", res.err)
		}
		vs.client = res.client
		vs.logger.Info("connected to Milvus successfully")
		return nil
	case <-ctx.Done():
		vs.logger.Warn("Milvus connection timeout")
		return fmt.Errorf("milvus connection timeout")
	}
}

func (vs *VectorStore) Connect(ctx context.Context) error {
	return vs.ensureConnected(ctx)
}

func (vs *VectorStore) ensureConnected(ctx context.Context) error {
	vs.mu.RLock()
	if vs.client != nil {
		vs.mu.RUnlock()
		return nil
	}
	vs.mu.RUnlock()
	vs.mu.Lock()
	defer vs.mu.Unlock()
	if vs.client != nil {
		return nil
	}
	return vs.doConnect(ctx)
}

func (vs *VectorStore) CreateCollection(ctx context.Context, collectionName string) error {
	return vs.CreateCollectionWithDim(ctx, collectionName, vs.dim)
}

// CreateCollectionWithDim creates a collection with a custom vector dimension.
// The schema includes a user_id field for per-user filtering.
// dim is cached in dimCache so Insert can pick it up without a signature change.
func (vs *VectorStore) CreateCollectionWithDim(ctx context.Context, collectionName string, dim int) error {
	if err := vs.ensureConnected(ctx); err != nil {
		return fmt.Errorf("milvus not available: %w", err)
	}
	vs.logger.Info("creating collection", zap.String("collection", collectionName), zap.Int("dim", dim))

	hasCollection, err := vs.client.HasCollection(ctx, collectionName)
	if err != nil {
		vs.logger.Error("failed to check collection", zap.Error(err))
		return fmt.Errorf("failed to check collection %s: %w", collectionName, err)
	}

	if hasCollection {
		existingDim, derr := vs.collectionDim(ctx, collectionName)
		if derr != nil {
			vs.logger.Warn("failed to describe existing collection, will reuse",
				zap.String("collection", collectionName), zap.Error(derr))
			vs.dimCache.Store(collectionName, dim)
			return nil
		}
		if existingDim == dim {
			if !vs.collectionHasField(ctx, collectionName, "agent_id") {
				vs.logger.Warn("collection missing required field, recreating",
					zap.String("collection", collectionName), zap.String("field", "agent_id"))
				if derr := vs.client.DropCollection(ctx, collectionName); derr != nil {
					return fmt.Errorf("failed to drop stale collection %s: %w", collectionName, derr)
				}
				vs.dimCache.Delete(collectionName)
				// fall through to recreate
			} else {
				vs.logger.Info("collection already exists",
					zap.String("collection", collectionName), zap.Int("dim", dim))
				vs.dimCache.Store(collectionName, dim)
				idxList, _ := vs.client.DescribeIndex(ctx, collectionName, "vector")
				if len(idxList) == 0 {
					flatIdx, ierr := entity.NewIndexFlat(entity.L2)
					if ierr == nil {
						_ = vs.client.CreateIndex(ctx, collectionName, "vector", flatIdx, false)
					}
				}
				return nil
			}
		} else {
			vs.logger.Warn("collection dim mismatch, recreating",
				zap.String("collection", collectionName),
				zap.Int("existing_dim", existingDim),
				zap.Int("required_dim", dim))
			if derr := vs.client.DropCollection(ctx, collectionName); derr != nil {
				return fmt.Errorf("failed to drop stale collection %s: %w", collectionName, derr)
			}
			vs.dimCache.Delete(collectionName)
		}
	}

	schema := &entity.Schema{
		CollectionName: collectionName,
		Description:    "memory collection",
		AutoID:         false,
		Fields: []*entity.Field{
			{
				Name:       "id",
				DataType:   entity.FieldTypeVarChar,
				PrimaryKey: true,
				TypeParams: map[string]string{"max_length": "65535"},
			},
			{
				Name:       "user_id",
				DataType:   entity.FieldTypeVarChar,
				TypeParams: map[string]string{"max_length": "128"},
			},
			{
				Name:       "agent_id",
				DataType:   entity.FieldTypeVarChar,
				TypeParams: map[string]string{"max_length": "128"},
			},
			{
				Name:       "scope",
				DataType:   entity.FieldTypeVarChar,
				TypeParams: map[string]string{"max_length": "20"},
			},
			{
				Name:       "content",
				DataType:   entity.FieldTypeVarChar,
				TypeParams: map[string]string{"max_length": "65535"},
			},
			{
				Name:       "source_document",
				DataType:   entity.FieldTypeVarChar,
				TypeParams: map[string]string{"max_length": "255"},
			},
			{
				Name:     "chunk_index",
				DataType: entity.FieldTypeInt64,
			},
			{
				Name:     "vector",
				DataType: entity.FieldTypeFloatVector,
				TypeParams: map[string]string{
					"dim": fmt.Sprintf("%d", dim),
				},
			},
		},
	}

	if err := vs.client.CreateCollection(ctx, schema, 2); err != nil {
		vs.logger.Error("failed to create collection", zap.String("collection", collectionName), zap.Error(err))
		return fmt.Errorf("failed to create collection %s: %w", collectionName, err)
	}
	idx, err := entity.NewIndexFlat(entity.L2)
	if err != nil {
		return fmt.Errorf("failed to build index param: %w", err)
	}
	if err := vs.client.CreateIndex(ctx, collectionName, "vector", idx, false); err != nil {
		return fmt.Errorf("failed to create index on %s: %w", collectionName, err)
	}
	vs.dimCache.Store(collectionName, dim)
	vs.logger.Info("collection created successfully", zap.String("collection", collectionName))
	return nil
}

func (vs *VectorStore) Insert(ctx context.Context, collectionName string, docs []DocumentChunk) error {
	if len(docs) == 0 {
		return nil
	}
	if err := vs.ensureConnected(ctx); err != nil {
		return fmt.Errorf("milvus not available: %w", err)
	}
	vs.logger.Debug("inserting vectors", zap.String("collection", collectionName), zap.Int("count", len(docs)))

	dim := vs.dim
	if cached, ok := vs.dimCache.Load(collectionName); ok {
		dim = cached.(int)
	}

	ids := make([]string, len(docs))
	userIDs := make([]string, len(docs))
	agentIDs := make([]string, len(docs))
	scopes := make([]string, len(docs))
	contents := make([]string, len(docs))
	sources := make([]string, len(docs))
	chunkIndices := make([]int64, len(docs))
	vectors := make([][]float32, len(docs))

	for i, doc := range docs {
		ids[i] = doc.ID
		userIDs[i] = doc.UserID
		agentIDs[i] = doc.AgentID
		scopes[i] = doc.Scope
		contents[i] = doc.Content
		sources[i] = doc.SourceDocument
		chunkIndices[i] = doc.ChunkIndex
		vectors[i] = doc.Vector
	}

	idCol := entity.NewColumnVarChar("id", ids)
	userIDCol := entity.NewColumnVarChar("user_id", userIDs)
	agentIDCol := entity.NewColumnVarChar("agent_id", agentIDs)
	scopeCol := entity.NewColumnVarChar("scope", scopes)
	contentCol := entity.NewColumnVarChar("content", contents)
	sourceCol := entity.NewColumnVarChar("source_document", sources)
	chunkIdxCol := entity.NewColumnInt64("chunk_index", chunkIndices)
	vectorCol := entity.NewColumnFloatVector("vector", dim, vectors)

	_, err := vs.client.Insert(ctx, collectionName, "", idCol, userIDCol, agentIDCol, scopeCol, contentCol, sourceCol, chunkIdxCol, vectorCol)
	if err != nil {
		vs.logger.Error("failed to insert vectors", zap.Error(err))
		return fmt.Errorf("failed to insert vectors: %w", err)
	}
	vs.logger.Info("vectors inserted successfully", zap.Int("count", len(docs)))
	return nil
}

func (vs *VectorStore) Search(ctx context.Context, collectionName string, queryVector []float32, topK int) ([]SearchResult, error) {
	return vs.SearchWithFilter(ctx, collectionName, queryVector, topK, "")
}

// SearchWithFilter performs vector search with an optional Milvus boolean expression filter.
// Pass expression="" for unfiltered search. Example: `user_id == "abc"` for per-user isolation.
func (vs *VectorStore) SearchWithFilter(ctx context.Context, collectionName string, queryVector []float32, topK int, expression string) ([]SearchResult, error) {
	if err := vs.ensureConnected(ctx); err != nil {
		return nil, fmt.Errorf("milvus not available: %w", err)
	}
	vs.logger.Debug("searching vectors", zap.String("collection", collectionName), zap.Int("topK", topK))

	if err := vs.client.LoadCollection(ctx, collectionName, false); err != nil {
		if strings.Contains(err.Error(), "index not found") {
			return []SearchResult{}, nil
		}
		vs.logger.Error("failed to load collection", zap.Error(err))
		return nil, fmt.Errorf("failed to load collection %s: %w", collectionName, err)
	}

	// Create search vector
	vectors := make([]entity.Vector, 1)
	vectors[0] = entity.FloatVector(queryVector)

	// Search parameters - L2 distance metric
	sp, err := entity.NewIndexFlatSearchParam()
	if err != nil {
		vs.logger.Error("failed to create search params", zap.Error(err))
		return nil, fmt.Errorf("failed to create search params: %w", err)
	}

	// Execute search
	results, searchErr := vs.client.Search(
		ctx,
		collectionName,
		[]string{}, // partition names
		expression,
		[]string{"id", "content", "source_document", "chunk_index"}, // output fields
		vectors,
		"vector",  // vector field name
		entity.L2, // metric type
		topK,
		sp,
	)
	if searchErr != nil {
		if strings.Contains(searchErr.Error(), "field agent_id not exist") {
			vs.logger.Warn("memory collection has stale schema, returning empty results",
				zap.String("collection", collectionName))
			return nil, nil
		}
		vs.logger.Error("failed to search vectors", zap.Error(searchErr))
		return nil, fmt.Errorf("failed to search vectors: %w", searchErr)
	}

	// Process results - returns []client.SearchResult
	searchResults := make([]SearchResult, 0)
	if len(results) > 0 {
		result := results[0]

		// Get columns from fields
		idCol := result.Fields.GetColumn("id")
		contentCol := result.Fields.GetColumn("content")
		sourceCol := result.Fields.GetColumn("source_document")
		chunkIdxCol := result.Fields.GetColumn("chunk_index")

		// Get scores from the result
		scores := result.Scores

		// Process each result (each result is one search with topK matches)
		for i := 0; i < result.ResultCount; i++ {
			var id, content, sourceDocument string
			var chunkIndex int64
			var score float32 = 0

			// Get ID
			if idCol != nil && i < idCol.Len() {
				if val, err := idCol.Get(i); err == nil {
					if idStr, ok := val.(string); ok {
						id = idStr
					}
				}
			}

			// Get content
			if contentCol != nil && i < contentCol.Len() {
				if val, err := contentCol.Get(i); err == nil {
					if contentStr, ok := val.(string); ok {
						content = contentStr
					}
				}
			}

			// Get source document
			if sourceCol != nil && i < sourceCol.Len() {
				if val, err := sourceCol.Get(i); err == nil {
					if sourceStr, ok := val.(string); ok {
						sourceDocument = sourceStr
					}
				}
			}

			// Get chunk index
			if chunkIdxCol != nil && i < chunkIdxCol.Len() {
				if val, err := chunkIdxCol.Get(i); err == nil {
					if idx, ok := val.(int64); ok {
						chunkIndex = idx
					}
				}
			}

			// Get score from result.Scores
			if i < len(scores) {
				score = float32(scores[i])
			}

			if id != "" && content != "" {
				searchResults = append(searchResults, SearchResult{
					ID:             id,
					Content:        content,
					SourceDocument: sourceDocument,
					ChunkIndex:     chunkIndex,
					Score:          score,
				})
			}
		}
	}

	vs.logger.Debug("search completed", zap.Int("results", len(searchResults)))
	return searchResults, nil
}

func (vs *VectorStore) Flush(ctx context.Context, collectionName string) error {
	if err := vs.ensureConnected(ctx); err != nil {
		return fmt.Errorf("milvus not available: %w", err)
	}
	vs.logger.Debug("flushing collection", zap.String("collection", collectionName))
	if err := vs.client.Flush(ctx, collectionName, false); err != nil {
		vs.logger.Error("failed to flush collection", zap.Error(err))
		return fmt.Errorf("failed to flush collection %s: %w", collectionName, err)
	}
	return nil
}

// DeleteByDocumentIDs removes all vectors whose source_document matches any of
// the given document IDs. Used when deleting a workspace to clean up vectors.
func (vs *VectorStore) DeleteByDocumentIDs(ctx context.Context, collectionName string, docIDs []string) error {
	if len(docIDs) == 0 {
		return nil
	}
	if err := vs.ensureConnected(ctx); err != nil {
		return fmt.Errorf("milvus not available: %w", err)
	}
	quoted := make([]string, len(docIDs))
	for i, id := range docIDs {
		quoted[i] = fmt.Sprintf("%q", id)
	}
	expr := fmt.Sprintf("source_document in [%s]", strings.Join(quoted, ","))
	vs.logger.Info("deleting vectors by document IDs",
		zap.String("collection", collectionName),
		zap.Int("doc_count", len(docIDs)))
	if err := vs.client.Delete(ctx, collectionName, "", expr); err != nil {
		return fmt.Errorf("failed to delete vectors: %w", err)
	}
	return nil
}

func (vs *VectorStore) DeleteCollection(ctx context.Context, collectionName string) error {
	if err := vs.ensureConnected(ctx); err != nil {
		return fmt.Errorf("milvus not available: %w", err)
	}
	exists, err := vs.client.HasCollection(ctx, collectionName)
	if err != nil {
		return fmt.Errorf("failed to check collection %s: %w", collectionName, err)
	}
	if !exists {
		return nil
	}
	if err := vs.client.DropCollection(ctx, collectionName); err != nil {
		return fmt.Errorf("failed to delete collection %s: %w", collectionName, err)
	}
	vs.logger.Info("collection deleted", zap.String("collection", collectionName))
	return nil
}

// KeywordSearch performs TF-IDF based keyword search on the collection.
// Fetches all documents and ranks them by relevance to query terms.
func (vs *VectorStore) KeywordSearch(ctx context.Context, collectionName string, query string, topK int) ([]SearchResult, error) {
	if err := vs.ensureConnected(ctx); err != nil {
		return nil, fmt.Errorf("milvus not available: %w", err)
	}
	vs.logger.Debug("keyword searching", zap.String("collection", collectionName), zap.String("query", query))

	if err := vs.client.LoadCollection(ctx, collectionName, false); err != nil {
		if strings.Contains(err.Error(), "index not found") {
			return []SearchResult{}, nil
		}
		return nil, fmt.Errorf("failed to load collection %s: %w", collectionName, err)
	}
	rows, err := vs.client.Query(
		ctx,
		collectionName,
		[]string{},
		"id != \"\"",
		[]string{"id", "content", "source_document", "chunk_index"},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query collection: %w", err)
	}

	idCol := rows.GetColumn("id")
	contentCol := rows.GetColumn("content")
	sourceCol := rows.GetColumn("source_document")
	chunkIdxCol := rows.GetColumn("chunk_index")

	if idCol == nil || contentCol == nil {
		return []SearchResult{}, nil
	}

	terms := tokenize(query)
	if len(terms) == 0 {
		return []SearchResult{}, nil
	}

	type candidate struct {
		SearchResult
		termFreqs map[string]int
		wordCount int
	}

	n := idCol.Len()
	candidates := make([]candidate, 0, n)

	for i := 0; i < n; i++ {
		var id, content, source string
		var chunkIdx int64

		if v, err := idCol.Get(i); err == nil {
			if s, ok := v.(string); ok {
				id = s
			}
		}
		if v, err := contentCol.Get(i); err == nil {
			if s, ok := v.(string); ok {
				content = s
			}
		}
		if sourceCol != nil {
			if v, err := sourceCol.Get(i); err == nil {
				if s, ok := v.(string); ok {
					source = s
				}
			}
		}
		if chunkIdxCol != nil {
			if v, err := chunkIdxCol.Get(i); err == nil {
				if idx, ok := v.(int64); ok {
					chunkIdx = idx
				}
			}
		}

		if id == "" || content == "" {
			continue
		}

		docTerms := tokenize(content)
		tf := make(map[string]int, len(docTerms))
		for _, t := range docTerms {
			tf[t]++
		}

		candidates = append(candidates, candidate{
			SearchResult: SearchResult{
				ID:             id,
				Content:        content,
				SourceDocument: source,
				ChunkIndex:     chunkIdx,
			},
			termFreqs: tf,
			wordCount: len(docTerms),
		})
	}

	// Calculate IDF: log((N+1)/(df+1)) + 1
	N := float64(len(candidates))
	idf := make(map[string]float64, len(terms))
	for _, t := range terms {
		df := 0
		for _, c := range candidates {
			if c.termFreqs[t] > 0 {
				df++
			}
		}
		idf[t] = math.Log((N+1)/(float64(df)+1)) + 1
	}

	// Calculate TF-IDF scores
	type scored struct {
		idx   int
		score float64
	}
	scores := make([]scored, 0, len(candidates))
	for i, c := range candidates {
		var s float64
		wc := float64(c.wordCount)
		if wc == 0 {
			continue
		}
		for _, t := range terms {
			tf := float64(c.termFreqs[t]) / wc
			s += tf * idf[t]
		}
		if s > 0 {
			scores = append(scores, scored{i, s})
		}
	}

	sort.Slice(scores, func(a, b int) bool { return scores[a].score > scores[b].score })

	if topK > len(scores) {
		topK = len(scores)
	}
	results := make([]SearchResult, topK)
	for i := 0; i < topK; i++ {
		r := candidates[scores[i].idx].SearchResult
		r.Score = float32(scores[i].score)
		results[i] = r
	}

	vs.logger.Debug("keyword search completed", zap.Int("results", len(results)))
	return results, nil
}

// HybridSearch performs RRF (Reciprocal Rank Fusion) of vector and keyword search results.
func (vs *VectorStore) HybridSearch(ctx context.Context, collectionName string, queryVector []float32, queryText string, topK int) ([]SearchResult, error) {
	vs.logger.Debug("hybrid searching", zap.String("collection", collectionName))

	// Run both searches in parallel
	type result struct {
		results []SearchResult
		err     error
	}
	vectorCh := make(chan result, 1)
	keywordCh := make(chan result, 1)

	go func() {
		r, err := vs.Search(ctx, collectionName, queryVector, topK*2)
		vectorCh <- result{r, err}
	}()

	go func() {
		r, err := vs.KeywordSearch(ctx, collectionName, queryText, topK*2)
		keywordCh <- result{r, err}
	}()

	vectorRes := <-vectorCh
	keywordRes := <-keywordCh

	if vectorRes.err != nil {
		return nil, fmt.Errorf("vector search failed: %w", vectorRes.err)
	}
	if keywordRes.err != nil {
		return nil, fmt.Errorf("keyword search failed: %w", keywordRes.err)
	}

	// RRF fusion with k=60 (standard parameter)
	const k = 60.0
	rrfScores := make(map[string]float64)

	for rank, r := range vectorRes.results {
		rrfScores[r.ID] += 1.0 / (k + float64(rank+1))
	}
	for rank, r := range keywordRes.results {
		rrfScores[r.ID] += 1.0 / (k + float64(rank+1))
	}

	// Collect unique results
	resultMap := make(map[string]SearchResult)
	for _, r := range vectorRes.results {
		resultMap[r.ID] = r
	}
	for _, r := range keywordRes.results {
		if _, exists := resultMap[r.ID]; !exists {
			resultMap[r.ID] = r
		}
	}

	// Sort by RRF score
	type scored struct {
		result SearchResult
		score  float64
	}
	scoredResults := make([]scored, 0, len(rrfScores))
	for id, score := range rrfScores {
		if r, ok := resultMap[id]; ok {
			scoredResults = append(scoredResults, scored{r, score})
		}
	}

	sort.Slice(scoredResults, func(i, j int) bool {
		return scoredResults[i].score > scoredResults[j].score
	})

	if topK > len(scoredResults) {
		topK = len(scoredResults)
	}
	results := make([]SearchResult, topK)
	for i := 0; i < topK; i++ {
		results[i] = scoredResults[i].result
		results[i].Score = float32(scoredResults[i].score)
	}

	vs.logger.Debug("hybrid search completed", zap.Int("results", len(results)))
	return results, nil
}

// tokenize splits text into lowercase word tokens, filtering punctuation.
func tokenize(text string) []string {
	text = strings.ToLower(text)
	var tokens []string
	var buf strings.Builder
	for _, r := range text {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r > 127 {
			buf.WriteRune(r)
		} else if buf.Len() > 0 {
			tokens = append(tokens, buf.String())
			buf.Reset()
		}
	}
	if buf.Len() > 0 {
		tokens = append(tokens, buf.String())
	}
	return tokens
}

// collectionDim reads the vector field dim from an existing collection's schema.
// Returns an error if the collection has no float-vector field or the dim is unparseable.
func (vs *VectorStore) collectionDim(ctx context.Context, collectionName string) (int, error) {
	coll, err := vs.client.DescribeCollection(ctx, collectionName)
	if err != nil {
		return 0, fmt.Errorf("describe collection %s: %w", collectionName, err)
	}
	if coll == nil || coll.Schema == nil {
		return 0, fmt.Errorf("collection %s has no schema", collectionName)
	}
	for _, f := range coll.Schema.Fields {
		if f.DataType != entity.FieldTypeFloatVector {
			continue
		}
		raw, ok := f.TypeParams["dim"]
		if !ok {
			return 0, fmt.Errorf("vector field of %s has no dim type-param", collectionName)
		}
		var d int
		if _, err := fmt.Sscanf(raw, "%d", &d); err != nil {
			return 0, fmt.Errorf("parse dim %q of %s: %w", raw, collectionName, err)
		}
		return d, nil
	}
	return 0, fmt.Errorf("collection %s has no float-vector field", collectionName)
}

func (vs *VectorStore) collectionHasField(ctx context.Context, collectionName, fieldName string) bool {
	coll, err := vs.client.DescribeCollection(ctx, collectionName)
	if err != nil || coll == nil || coll.Schema == nil {
		return false
	}
	for _, f := range coll.Schema.Fields {
		if f.Name == fieldName {
			return true
		}
	}
	return false
}

func (vs *VectorStore) Close() error {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	vs.logger.Info("closing Milvus connection")
	if vs.client != nil {
		err := vs.client.Close()
		vs.client = nil
		return err
	}
	return nil
}
