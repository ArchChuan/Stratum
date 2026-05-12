package document

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ledongthuc/pdf"
	"github.com/unidoc/unioffice/document"
	"go.uber.org/zap"
)

type Parser struct {
	logger *zap.Logger
}

func NewParser(logger *zap.Logger) *Parser {
	return &Parser{logger: logger}
}

type ParsedDocument struct {
	Content string
	Title   string
	Format  string
}

func (p *Parser) ParseFile(filePath string) (*ParsedDocument, error) {
	p.logger.Info("parsing document", zap.String("path", filePath))

	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".pdf":
		return p.parsePDF(filePath)
	case ".docx", ".doc":
		return p.parseDOCX(filePath)
	case ".txt":
		return p.parseTXT(filePath)
	case ".md":
		return p.parseMarkdown(filePath)
	default:
		return nil, fmt.Errorf("unsupported file format: %s", ext)
	}
}

func (p *Parser) parsePDF(filePath string) (*ParsedDocument, error) {
	f, err := pdf.Open(filePath)
	if err != nil {
		p.logger.Error("failed to open PDF", zap.String("path", filePath), zap.Error(err))
		return nil, fmt.Errorf("failed to open PDF: %w", err)
	}
	defer f.Close()

	var buf bytes.Buffer
	totalPages := f.NumPage()

	for i := 1; i <= totalPages; i++ {
		pages, err := f.GetPageN(i)
		if err != nil {
			p.logger.Warn("failed to get page", zap.Int("page", i), zap.Error(err))
			continue
		}
		text, err := pages.GetPlainText()
		if err != nil {
			p.logger.Warn("failed to extract text", zap.Int("page", i), zap.Error(err))
			continue
		}
		buf.WriteString(text)
		buf.WriteString("\n\n")
	}

	content := buf.String()
	p.logger.Info("PDF parsed", zap.Int("pages", totalPages), zap.Int("content_length", len(content)))

	return &ParsedDocument{
		Content: content,
		Title:   filepath.Base(filePath),
		Format:  "pdf",
	}, nil
}

func (p *Parser) parseDOCX(filePath string) (*ParsedDocument, error) {
	doc, err := document.Open(filePath)
	if err != nil {
		p.logger.Error("failed to open DOCX", zap.String("path", filePath), zap.Error(err))
		return nil, fmt.Errorf("failed to open DOCX: %w", err)
	}
	defer doc.Close()

	paragraphs := doc.Paragraphs()
	var content strings.Builder

	for _, para := range paragraphs {
		content.WriteString(para.Text)
		content.WriteString("\n")
	}

	p.logger.Info("DOCX parsed", zap.Int("paragraphs", len(paragraphs)))

	return &ParsedDocument{
		Content: content.String(),
		Title:   filepath.Base(filePath),
		Format:  "docx",
	}, nil
}

func (p *Parser) parseTXT(filePath string) (*ParsedDocument, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		p.logger.Error("failed to read TXT", zap.String("path", filePath), zap.Error(err))
		return nil, fmt.Errorf("failed to read TXT: %w", err)
	}

	p.logger.Info("TXT parsed", zap.Int("content_length", len(content)))

	return &ParsedDocument{
		Content: string(content),
		Title:   filepath.Base(filePath),
		Format:  "txt",
	}, nil
}

func (p *Parser) parseMarkdown(filePath string) (*ParsedDocument, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		p.logger.Error("failed to read Markdown", zap.String("path", filePath), zap.Error(err))
		return nil, fmt.Errorf("failed to read Markdown: %w", err)
	}

	p.logger.Info("Markdown parsed", zap.Int("content_length", len(content)))

	return &ParsedDocument{
		Content: string(content),
		Title:   filepath.Base(filePath),
		Format:  "md",
	}, nil
}

func (p *Parser) ParseBytes(data []byte, filename string) (*ParsedDocument, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".pdf":
		return p.parsePDFBytes(data, filename)
	case ".docx", ".doc":
		return p.parseDOCXBytes(data, filename)
	case ".txt":
		return &ParsedDocument{
			Content: string(data),
			Title:   filename,
			Format:  "txt",
		}, nil
	case ".md":
		return &ParsedDocument{
			Content: string(data),
			Title:   filename,
			Format:  "md",
		}, nil
	default:
		return nil, fmt.Errorf("unsupported file format: %s", ext)
	}
}

func (p *Parser) parsePDFBytes(data []byte, filename string) (*ParsedDocument, error) {
	f, err := pdf.New(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("failed to parse PDF bytes: %w", err)
	}
	defer f.Close()

	var buf bytes.Buffer
	totalPages := f.NumPage()

	for i := 1; i <= totalPages; i++ {
		pages, err := f.GetPageN(i)
		if err != nil {
			continue
		}
		text, err := pages.GetPlainText()
		if err != nil {
			continue
		}
		buf.WriteString(text)
		buf.WriteString("\n\n")
	}

	return &ParsedDocument{
		Content: buf.String(),
		Title:   filename,
		Format:  "pdf",
	}, nil
}

func (p *Parser) parseDOCXBytes(data []byte, filename string) (*ParsedDocument, error) {
	doc, err := document.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("failed to parse DOCX bytes: %w", err)
	}
	defer doc.Close()

	paragraphs := doc.Paragraphs()
	var content strings.Builder

	for _, para := range paragraphs {
		content.WriteString(para.Text)
		content.WriteString("\n")
	}

	return &ParsedDocument{
		Content: content.String(),
		Title:   filename,
		Format:  "docx",
	}, nil
}
