// Package vector provides vector database integration.

package vector

import (
	"context"
	"fmt"
	"math"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/milvus-io/milvus-sdk-go/v2/client"
	"github.com/milvus-io/milvus-sdk-go/v2/entity"
	"go.uber.org/zap"
)

type VectorStore struct {
	client client.Client
	host   string
	port   string
	logger *zap.Logger
	dim    int
}

func NewVectorStore(host, port string, logger *zap.Logger) *VectorStore {
	return &VectorStore{
		host:   host,
		port:   port,
		logger: logger,
		dim:    1536,
	}
}

func (vs *VectorStore) Connect(ctx context.Context) error {
	vs.logger.Info("connecting to Milvus", zap.String("host", vs.host), zap.String("port", vs.port))
	milvusAddr := fmt.Sprintf("%s:%s", vs.host, vs.port)

	// First check if the port is reachable using net.Dialer with timeout
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", milvusAddr)
	if err != nil {
		vs.logger.Warn("Milvus port not reachable", zap.Error(err))
		return fmt.Errorf("milvus port not reachable: %w", err)
	}
	conn.Close() //nolint:errcheck,gosec

	// Now try to create gRPC client
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

func (vs *VectorStore) CreateCollection(ctx context.Context, collectionName string) error {
	vs.logger.Info("creating collection", zap.String("collection", collectionName))

	hasCollection, err := vs.client.HasCollection(ctx, collectionName)
	if err != nil {
		vs.logger.Error("failed to check collection", zap.Error(err))
		return fmt.Errorf("failed to check collection %s: %w", collectionName, err)
	}

	if hasCollection {
		vs.logger.Info("collection already exists", zap.String("collection", collectionName))
		return nil
	}

	schema := &entity.Schema{
		CollectionName: collectionName,
		Description:    "RAG knowledge collection",
		AutoID:         false,
		Fields: []*entity.Field{
			{
				Name:       "id",
				DataType:   entity.FieldTypeVarChar,
				PrimaryKey: true,
				TypeParams: map[string]string{"max_length": "65535"},
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
					"dim": fmt.Sprintf("%d", vs.dim),
				},
			},
		},
	}

	if err := vs.client.CreateCollection(ctx, schema, 2); err != nil {
		vs.logger.Error("failed to create collection", zap.String("collection", collectionName), zap.Error(err))
		return fmt.Errorf("failed to create collection %s: %w", collectionName, err)
	}
	vs.logger.Info("collection created successfully")
	return nil
}

func (vs *VectorStore) Insert(ctx context.Context, collectionName string, docs []DocumentChunk) error {
	if len(docs) == 0 {
		return nil
	}
	vs.logger.Debug("inserting vectors", zap.String("collection", collectionName), zap.Int("count", len(docs)))

	ids := make([]string, len(docs))
	contents := make([]string, len(docs))
	sources := make([]string, len(docs))
	chunkIndices := make([]int64, len(docs))
	vectors := make([][]float32, len(docs))

	for i, doc := range docs {
		ids[i] = doc.ID
		contents[i] = doc.Content
		sources[i] = doc.SourceDocument
		chunkIndices[i] = doc.ChunkIndex
		vectors[i] = doc.Vector
	}

	idCol := entity.NewColumnVarChar("id", ids)
	contentCol := entity.NewColumnVarChar("content", contents)
	sourceCol := entity.NewColumnVarChar("source_document", sources)
	chunkIdxCol := entity.NewColumnInt64("chunk_index", chunkIndices)
	vectorCol := entity.NewColumnFloatVector("vector", vs.dim, vectors)

	_, err := vs.client.Insert(ctx, collectionName, "", idCol, contentCol, sourceCol, chunkIdxCol, vectorCol)
	if err != nil {
		vs.logger.Error("failed to insert vectors", zap.Error(err))
		return fmt.Errorf("failed to insert vectors: %w", err)
	}
	vs.logger.Info("vectors inserted successfully", zap.Int("count", len(docs)))
	return nil
}

func (vs *VectorStore) Search(ctx context.Context, collectionName string, queryVector []float32, topK int) ([]SearchResult, error) {
	vs.logger.Debug("searching vectors", zap.String("collection", collectionName), zap.Int("topK", topK))

	if err := vs.client.LoadCollection(ctx, collectionName, false); err != nil {
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
		"",         // expression (empty means no filtering)
		[]string{"id", "content", "source_document", "chunk_index"}, // output fields
		vectors,
		"vector",  // vector field name
		entity.L2, // metric type
		topK,
		sp,
	)
	if searchErr != nil {
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
	vs.logger.Debug("flushing collection", zap.String("collection", collectionName))
	if err := vs.client.Flush(ctx, collectionName, false); err != nil {
		vs.logger.Error("failed to flush collection", zap.Error(err))
		return fmt.Errorf("failed to flush collection %s: %w", collectionName, err)
	}
	return nil
}

func (vs *VectorStore) DeleteCollection(ctx context.Context, collectionName string) error {
	vs.logger.Info("deleting collection", zap.String("collection", collectionName))
	if err := vs.client.DropCollection(ctx, collectionName); err != nil {
		vs.logger.Error("failed to delete collection", zap.String("collection", collectionName), zap.Error(err))
		return fmt.Errorf("failed to delete collection %s: %w", collectionName, err)
	}
	vs.logger.Info("collection deleted successfully")
	return nil
}

// KeywordSearch performs TF-IDF based keyword search on the collection.
// Fetches all documents and ranks them by relevance to query terms.
func (vs *VectorStore) KeywordSearch(ctx context.Context, collectionName string, query string, topK int) ([]SearchResult, error) {
	vs.logger.Debug("keyword searching", zap.String("collection", collectionName), zap.String("query", query))

	if err := vs.client.LoadCollection(ctx, collectionName, false); err != nil {
		return nil, fmt.Errorf("failed to load collection %s: %w", collectionName, err)
	}

	// Query all documents
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

func (vs *VectorStore) Close() error {
	vs.logger.Info("closing Milvus connection")
	if vs.client != nil {
		return vs.client.Close()
	}
	return nil
}

type DocumentChunk struct {
	ID             string
	Content        string
	SourceDocument string
	ChunkIndex     int64
	Vector         []float32
}

type SearchResult struct {
	ID             string
	Content        string
	SourceDocument string
	ChunkIndex     int64
	Score          float32
}
