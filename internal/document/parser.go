// Package document provides document parsing and processing.
package document

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ledongthuc/pdf"
	"github.com/unidoc/unioffice/document"
	"go.uber.org/zap"
)

type Parser struct {
	logger *zap.Logger
}

func NewParser(logger *zap.Logger) *Parser {
	return &Parser{
		logger: logger,
	}
}

func (p *Parser) ParseFile(filePath string) (string, error) {
	p.logger.Debug("parsing file", zap.String("path", filePath))

	switch {
	case strings.HasSuffix(filePath, ".pdf"):
		return p.parsePDF(filePath)
	case strings.HasSuffix(filePath, ".docx"):
		return p.parseDOCX(filePath)
	case strings.HasSuffix(filePath, ".txt"), strings.HasSuffix(filePath, ".md"):
		return p.parseTXT(filePath)
	default:
		return "", fmt.Errorf("unsupported file type: %s", filePath)
	}
}

// ParseBytes parses document bytes. The hint parameter accepts either a file name
// (e.g. "report.pdf") or a MIME type (e.g. "application/pdf").
func (p *Parser) ParseBytes(data []byte, hint string) (string, error) {
	p.logger.Debug("parsing bytes", zap.String("hint", hint))

	lower := strings.ToLower(hint)

	// Detect by MIME type when hint contains "/"
	if strings.Contains(lower, "/") {
		switch lower {
		case "application/pdf":
			return p.parsePDFBytes(data)
		case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
			return p.parseDOCXBytes(data)
		case "text/plain", "text/markdown":
			return string(data), nil
		default:
			return "", fmt.Errorf("unsupported content type: %s", hint)
		}
	}

	// Detect by file extension
	switch {
	case strings.HasSuffix(lower, ".pdf"):
		return p.parsePDFBytes(data)
	case strings.HasSuffix(lower, ".docx"):
		return p.parseDOCXBytes(data)
	case strings.HasSuffix(lower, ".txt"), strings.HasSuffix(lower, ".md"):
		return string(data), nil
	default:
		return "", fmt.Errorf("unsupported file type: %s", hint)
	}
}

func (p *Parser) parsePDF(filePath string) (string, error) {
	f, r, err := pdf.Open(filePath)
	if err != nil {
		p.logger.Error("failed to open PDF", zap.Error(err))
		return "", fmt.Errorf("failed to open PDF: %w", err)
	}
	defer f.Close()

	var buf bytes.Buffer
	contentReader, err := r.GetPlainText()
	if err != nil {
		p.logger.Error("failed to get PDF text", zap.Error(err))
		return "", fmt.Errorf("failed to get PDF text: %w", err)
	}
	if _, err := io.Copy(&buf, contentReader); err != nil {
		return "", fmt.Errorf("failed to read PDF content: %w", err)
	}

	return buf.String(), nil
}

func (p *Parser) parsePDFBytes(data []byte) (string, error) {
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		p.logger.Error("failed to read PDF bytes", zap.Error(err))
		return "", fmt.Errorf("failed to read PDF bytes: %w", err)
	}

	var buf bytes.Buffer
	contentReader, err := r.GetPlainText()
	if err != nil {
		p.logger.Error("failed to get PDF text", zap.Error(err))
		return "", fmt.Errorf("failed to get PDF text: %w", err)
	}
	if _, err := io.Copy(&buf, contentReader); err != nil {
		return "", fmt.Errorf("failed to read PDF content: %w", err)
	}

	return buf.String(), nil
}

func (p *Parser) parseDOCX(filePath string) (string, error) {
	doc, err := document.Open(filePath)
	if err != nil {
		p.logger.Error("failed to open DOCX", zap.Error(err))
		return "", fmt.Errorf("failed to open DOCX: %w", err)
	}
	defer doc.Close()

	var textBuilder strings.Builder
	for _, para := range doc.Paragraphs() {
		for _, run := range para.Runs() {
			textBuilder.WriteString(run.Text())
		}
		textBuilder.WriteString("\n")
	}

	return textBuilder.String(), nil
}

func (p *Parser) parseDOCXBytes(data []byte) (string, error) {
	r := bytes.NewReader(data)
	doc, err := document.Read(r, int64(len(data)))
	if err != nil {
		p.logger.Error("failed to read DOCX bytes", zap.Error(err))
		return "", fmt.Errorf("failed to read DOCX bytes: %w", err)
	}
	defer doc.Close()

	var textBuilder strings.Builder
	for _, para := range doc.Paragraphs() {
		for _, run := range para.Runs() {
			textBuilder.WriteString(run.Text())
		}
		textBuilder.WriteString("\n")
	}

	return textBuilder.String(), nil
}

func (p *Parser) parseTXT(filePath string) (string, error) {
	file, err := os.ReadFile(filePath)
	if err != nil {
		p.logger.Error("failed to read TXT", zap.Error(err))
		return "", fmt.Errorf("failed to read TXT: %w", err)
	}

	return string(file), nil
}
