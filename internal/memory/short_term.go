// Package memory provides agent memory management.
package memory

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ConversationBufferMemory stores full conversation history
// Reference: LangChain ConversationBufferMemory
type ConversationBufferMemory struct {
	memory []*MemoryEntry
	config *MemoryConfig
	logger *zap.Logger
	mu     sync.RWMutex
}

// NewConversationBufferMemory creates a new buffer-based memory
func NewConversationBufferMemory(config *MemoryConfig, logger *zap.Logger) *ConversationBufferMemory {
	if config == nil {
		config = DefaultMemoryConfig()
	}
	return &ConversationBufferMemory{
		memory: make([]*MemoryEntry, 0),
		config: config,
		logger: logger,
	}
}

// Add adds a message to the conversation buffer
func (m *ConversationBufferMemory) Add(ctx context.Context, entry *MemoryEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if entry.ID == "" {
		entry.ID = uuid.New().String()
	}
	entry.Type = ShortTermMemory
	entry.Timestamp = time.Now()

	m.memory = append(m.memory, entry)

	// Enforce max messages limit
	if len(m.memory) > m.config.MaxShortTermMessages {
		// Remove oldest entries
		removed := len(m.memory) - m.config.MaxShortTermMessages
		m.memory = m.memory[removed:]
		m.logger.Debug("trimmed old memory entries",
			zap.Int("removed", removed),
			zap.Int("remaining", len(m.memory)))
	}

	return nil
}

// Get retrieves a memory entry by ID
func (m *ConversationBufferMemory) Get(ctx context.Context, id string) (*MemoryEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, entry := range m.memory {
		if entry.ID == id {
			return entry, nil
		}
	}
	return nil, fmt.Errorf("memory entry not found: %s", id)
}

// Search searches memory entries
func (m *ConversationBufferMemory) Search(ctx context.Context, req *MemorySearchRequest) ([]*MemorySearchResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []*MemorySearchResult

	for _, entry := range m.memory {
		// Apply filters
		if req.Context != nil {
			if req.Context.TenantID != "" && entry.TenantID != req.Context.TenantID {
				continue
			}
			if req.Context.UserID != "" && entry.UserID != req.Context.UserID {
				continue
			}
			if req.Context.SessionID != "" && entry.SessionID != req.Context.SessionID {
				continue
			}
		}

		// Filter by type
		if len(req.Types) > 0 {
			typeMatch := false
			for _, t := range req.Types {
				if entry.Type == t {
					typeMatch = true
					break
				}
			}
			if !typeMatch {
				continue
			}
		}

		// Filter by time range
		if req.TimeRange != nil {
			if entry.Timestamp.Before(req.TimeRange.From) || entry.Timestamp.After(req.TimeRange.To) {
				continue
			}
		}

		// Simple keyword matching
		score := 1.0 // Default score for buffer memory
		if req.Query != "" {
			// Very simple scoring based on content match
			if contains(entry.Content, req.Query) {
				score = 0.9
			} else {
				continue // Skip if query doesn't match
			}
		}

		results = append(results, &MemorySearchResult{
			Entry: entry,
			Score: score,
		})

		// Apply limit
		if req.Limit > 0 && len(results) >= req.Limit {
			break
		}
	}

	return results, nil
}

// Delete removes a memory entry
func (m *ConversationBufferMemory) Delete(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i, entry := range m.memory {
		if entry.ID == id {
			m.memory = append(m.memory[:i], m.memory[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("memory entry not found: %s", id)
}

// Clear removes all memory entries for a session
func (m *ConversationBufferMemory) Clear(ctx context.Context, sessionCtx *SessionContext) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if sessionCtx == nil {
		m.memory = make([]*MemoryEntry, 0)
		return nil
	}

	// Keep only entries that don't match the session
	var filtered []*MemoryEntry
	for _, entry := range m.memory {
		if entry.SessionID != sessionCtx.SessionID {
			filtered = append(filtered, entry)
		}
	}
	m.memory = filtered

	return nil
}

// GetStats returns memory statistics
func (m *ConversationBufferMemory) GetStats(ctx context.Context, sessionCtx *SessionContext) (*MemoryStats, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := len(m.memory)
	if sessionCtx != nil {
		count = 0
		for _, entry := range m.memory {
			if entry.SessionID == sessionCtx.SessionID {
				count++
			}
		}
	}

	return &MemoryStats{
		TotalEntries:   int64(count),
		ShortTermCount: int64(count),
		LastAccessTime: time.Now(),
	}, nil
}

// Cleanup removes expired entries
func (m *ConversationBufferMemory) Cleanup(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	var filtered []*MemoryEntry

	for _, entry := range m.memory {
		if !entry.ExpiresAt.IsZero() && entry.ExpiresAt.Before(now) {
			continue
		}
		if now.Sub(entry.Timestamp) > m.config.MaxMemoryAge {
			continue
		}
		filtered = append(filtered, entry)
	}

	m.memory = filtered
	return nil
}

// GetMemory returns all memory entries
func (m *ConversationBufferMemory) GetMemory() []*MemoryEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.memory
}

// ConversationWindowMemory stores a sliding window of recent messages
// Reference: LangChain ConversationBufferWindowMemory
type ConversationWindowMemory struct {
	*ConversationBufferMemory
	windowSize int
}

// NewConversationWindowMemory creates a new window-based memory
func NewConversationWindowMemory(config *MemoryConfig, logger *zap.Logger) *ConversationWindowMemory {
	windowSize := config.ShortTermWindowSize
	if windowSize <= 0 {
		windowSize = 10 // Default window size
	}
	return &ConversationWindowMemory{
		ConversationBufferMemory: NewConversationBufferMemory(config, logger),
		windowSize:               windowSize,
	}
}

// Add adds a message to the conversation window
func (m *ConversationWindowMemory) Add(ctx context.Context, entry *MemoryEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if entry.ID == "" {
		entry.ID = uuid.New().String()
	}
	entry.Type = ShortTermMemory
	entry.Timestamp = time.Now()

	m.memory = append(m.memory, entry)

	// Keep only the last N messages
	if len(m.memory) > m.windowSize {
		m.memory = m.memory[len(m.memory)-m.windowSize:]
	}

	return nil
}

// ConversationSummaryMemory maintains a summary of the conversation
// Reference: LangChain ConversationSummaryMemory
type ConversationSummaryMemory struct {
	*ConversationBufferMemory
	summary     string
	fullHistory []*MemoryEntry
	config      *MemoryConfig
	logger      *zap.Logger
	mu          sync.RWMutex
}

// NewConversationSummaryMemory creates a new summary-based memory
func NewConversationSummaryMemory(config *MemoryConfig, logger *zap.Logger) *ConversationSummaryMemory {
	if config == nil {
		config = DefaultMemoryConfig()
	}
	return &ConversationSummaryMemory{
		fullHistory: make([]*MemoryEntry, 0),
		config:      config,
		logger:      logger,
	}
}

// Add adds a message to the summary memory
func (m *ConversationSummaryMemory) Add(ctx context.Context, entry *MemoryEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if entry.ID == "" {
		entry.ID = uuid.New().String()
	}
	entry.Type = SummaryMemory
	entry.Timestamp = time.Now()

	m.fullHistory = append(m.fullHistory, entry)

	// Update summary periodically
	if len(m.fullHistory)%5 == 0 {
		m.updateSummary()
	}

	return nil
}

// updateSummary generates or updates the conversation summary
func (m *ConversationSummaryMemory) updateSummary() {
	// Simple summary implementation - in production, use LLM to generate summary
	if len(m.fullHistory) == 0 {
		return
	}

	recent := m.fullHistory
	if len(recent) > 20 {
		recent = m.fullHistory[len(recent)-20:]
	}

	// Create a simple summary
	userMessages := 0
	assistantMessages := 0
	for _, entry := range recent {
		switch entry.Role {
		case "user":
			userMessages++
		case "assistant":
			assistantMessages++
		}
	}

	m.summary = fmt.Sprintf("Conversation with %d user messages and %d assistant responses. "+
		"Most recent interaction: %s",
		userMessages, assistantMessages, m.getLastUserMessage())
}

func (m *ConversationSummaryMemory) getLastUserMessage() string {
	for i := len(m.fullHistory) - 1; i >= 0; i-- {
		if m.fullHistory[i].Role == "user" {
			return m.fullHistory[i].Content
		}
	}
	return "N/A"
}

// GetSummary returns the conversation summary
func (m *ConversationSummaryMemory) GetSummary() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.summary
}

// Search searches in summary memory
func (m *ConversationSummaryMemory) Search(ctx context.Context, req *MemorySearchRequest) ([]*MemorySearchResult, error) {
	// Use the embedded buffer memory's search
	return m.ConversationBufferMemory.Search(ctx, req)
}

// Implement other Memory interface methods
func (m *ConversationSummaryMemory) Get(ctx context.Context, id string) (*MemoryEntry, error) {
	return m.ConversationBufferMemory.Get(ctx, id)
}

func (m *ConversationSummaryMemory) Delete(ctx context.Context, id string) error {
	return m.ConversationBufferMemory.Delete(ctx, id)
}

func (m *ConversationSummaryMemory) Clear(ctx context.Context, sessionCtx *SessionContext) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.summary = ""
	m.fullHistory = make([]*MemoryEntry, 0)
	return nil
}

func (m *ConversationSummaryMemory) GetStats(ctx context.Context, sessionCtx *SessionContext) (*MemoryStats, error) {
	return m.ConversationBufferMemory.GetStats(ctx, sessionCtx)
}

func (m *ConversationSummaryMemory) Cleanup(ctx context.Context) error {
	return m.ConversationBufferMemory.Cleanup(ctx)
}

// contains checks if a string contains a substring (case-insensitive)
func contains(s, substr string) bool {
	s = toLower(s)
	substr = toLower(substr)
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && containsSubstring(s, substr))
}

func toLower(s string) string {
	result := make([]rune, len(s))
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			result[i] = r + ('a' - 'A')
		} else {
			result[i] = r
		}
	}
	return string(result)
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			if s[i+j] != substr[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
