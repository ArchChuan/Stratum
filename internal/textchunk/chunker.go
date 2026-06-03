// Package textchunk provides text chunking utilities.
package textchunk

import (
	"strings"

	"go.uber.org/zap"
)

type Chunker struct {
	logger       *zap.Logger
	maxChunkSize int
	overlapSize  int
	minChunkSize int
}

func NewChunker(logger *zap.Logger) *Chunker {
	return &Chunker{
		logger:       logger,
		maxChunkSize: 1000, // Maximum characters per chunk
		overlapSize:  200,  // Overlap between chunks
		minChunkSize: 100,  // Minimum characters per chunk
	}
}

type TextChunk struct {
	Content    string
	Index      int
	SourceText string
}

func (c *Chunker) ChunkText(text string) []TextChunk {
	c.logger.Debug("chunking text", zap.Int("text_length", len(text)))

	if len(text) <= c.maxChunkSize {
		return []TextChunk{
			{
				Content:    text,
				Index:      0,
				SourceText: text,
			},
		}
	}

	var chunks []TextChunk
	chunkIndex := 0
	position := 0

	for position < len(text) {
		endPosition := position + c.maxChunkSize
		if endPosition > len(text) {
			endPosition = len(text)
		}

		chunk := c.findChunkBoundary(text, position, endPosition)
		chunkContent := text[position:chunk]

		if len(chunkContent) < c.minChunkSize {
			c.logger.Warn("chunk too small, skipping", zap.Int("chunk_size", len(chunkContent)))
			position = endPosition
			continue
		}

		chunks = append(chunks, TextChunk{
			Content:    chunkContent,
			Index:      chunkIndex,
			SourceText: text,
		})
		chunkIndex++

		position = chunk - c.overlapSize
		if position < 0 {
			position = 0
		}
	}

	c.logger.Info("text chunked", zap.Int("num_chunks", len(chunks)))
	return chunks
}

func (c *Chunker) findChunkBoundary(text string, start, end int) int {
	if end >= len(text) {
		return end
	}

	sentences := c.splitSentences(text[start:end])
	if len(sentences) <= 1 {
		return end
	}

	accumulatedLength := 0
	for i, sentence := range sentences {
		if accumulatedLength+len(sentence) > c.maxChunkSize && i > 0 {
			return start + accumulatedLength
		}
		accumulatedLength += len(sentence)
	}

	return start + accumulatedLength
}

func (c *Chunker) splitSentences(text string) []string {
	var sentences []string
	var currentSentence strings.Builder

	for _, r := range text {
		if r == '。' || r == '！' || r == '？' || r == '．' {
			currentSentence.WriteRune(r)
			if currentSentence.Len() > 0 {
				sentences = append(sentences, currentSentence.String())
				currentSentence.Reset()
			}
		} else if r == '.' || r == '!' || r == '?' {
			currentSentence.WriteRune(r)
			// Check if this is likely end of a Latin sentence
			if c.isLatinChar(r) {
				sentences = append(sentences, currentSentence.String())
				currentSentence.Reset()
			}
		} else if r == '\n' {
			if currentSentence.Len() > 0 {
				sentences = append(sentences, currentSentence.String())
				currentSentence.Reset()
			}
		} else {
			currentSentence.WriteRune(r)
		}
	}

	if currentSentence.Len() > 0 {
		sentences = append(sentences, currentSentence.String())
	}

	return sentences
}

func (c *Chunker) isLatinChar(r rune) bool {
	return (r >= 0x0041 && r <= 0x007A) ||
		(r >= 0x00C0 && r <= 0x00D6) ||
		(r >= 0x0030 && r <= 0x0039)
}

func (c *Chunker) ChunkByParagraphs(text string) []TextChunk {
	c.logger.Debug("chunking by paragraphs")

	paragraphs := strings.Split(text, "\n")
	var chunks []TextChunk
	chunkIndex := 0
	var currentChunk strings.Builder
	currentChunkSize := 0

	for _, para := range paragraphs {
		para = strings.TrimSpace(para)
		if len(para) == 0 {
			continue
		}

		if currentChunkSize+len(para) > c.maxChunkSize && currentChunkSize > 0 {
			chunks = append(chunks, TextChunk{
				Content:    strings.TrimSpace(currentChunk.String()),
				Index:      chunkIndex,
				SourceText: text,
			})
			chunkIndex++
			currentChunk.Reset()
			currentChunkSize = 0
		}

		currentChunk.WriteString(para + "\n")
		currentChunkSize += len(para) + 1
	}

	if currentChunkSize > 0 {
		chunks = append(chunks, TextChunk{
			Content:    strings.TrimSpace(currentChunk.String()),
			Index:      chunkIndex,
			SourceText: text,
		})
	}

	c.logger.Info("paragraphs chunked", zap.Int("num_chunks", len(chunks)))
	return chunks
}

func (c *Chunker) ChunkBySemanticBreaks(text string) []TextChunk {
	c.logger.Debug("chunking by semantic breaks")

	breaks := []string{
		"\n\n",
		"----------------------------------------",
		"========================================",
		"=================================",
		"======================================",
	}

	var chunks []TextChunk
	start := 0

	for _, breakStr := range breaks {
		for {
			idx := strings.Index(text[start:], breakStr)
			if idx == -1 {
				break
			}
			idx += start
			if idx > start && idx-start > c.minChunkSize {
				chunks = append(chunks, TextChunk{
					Content:    strings.TrimSpace(text[start:idx]),
					Index:      len(chunks),
					SourceText: text,
				})
				start = idx + len(breakStr)
			} else {
				break
			}
		}
	}

	if start < len(text) {
		chunks = append(chunks, TextChunk{
			Content:    strings.TrimSpace(text[start:]),
			Index:      len(chunks),
			SourceText: text,
		})
	}

	c.logger.Info("semantic chunking completed", zap.Int("num_chunks", len(chunks)))
	return chunks
}

func (c *Chunker) SmartChunk(text string) []TextChunk {
	c.logger.Debug("performing smart chunking")

	paragraphs := c.ChunkByParagraphs(text)
	if len(paragraphs) == 1 {
		sentences := c.ChunkText(text)
		if len(sentences) == 1 {
			return c.ChunkBySemanticBreaks(text)
		}
		return sentences
	}

	var finalChunks []TextChunk
	for _, para := range paragraphs {
		if len(para.Content) > c.maxChunkSize {
			subChunks := c.ChunkText(para.Content)
			for _, sub := range subChunks {
				finalChunks = append(finalChunks, TextChunk{
					Content:    sub.Content,
					Index:      len(finalChunks),
					SourceText: text,
				})
			}
		} else {
			finalChunks = append(finalChunks, para)
		}
	}

	c.logger.Info("smart chunking completed", zap.Int("num_chunks", len(finalChunks)))
	return finalChunks
}
