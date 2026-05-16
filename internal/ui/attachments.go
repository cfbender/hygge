package ui

import (
	"encoding/base64"
	"fmt"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/cfbender/hygge/internal/session"
)

const maxPromptAttachmentTextBytes = 64 * 1024

type promptAttachment struct {
	Path     string
	Name     string
	Size     int64
	MimeType string
	Parts    []session.Part
}

func loadPromptAttachment(path string) (promptAttachment, error) {
	return loadPromptAttachmentWithLimit(path, maxPromptAttachmentTextBytes)
}

func loadMentionPromptAttachment(path string) (promptAttachment, error) {
	return loadPromptAttachmentWithLimit(path, 0)
}

func loadPromptAttachmentWithLimit(path string, maxTextBytes int64) (promptAttachment, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return promptAttachment{}, fmt.Errorf("path is required")
	}
	if u, err := url.Parse(path); err == nil && u.Scheme != "" {
		return promptAttachment{}, fmt.Errorf("URLs are not supported; use a local file path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return promptAttachment{}, fmt.Errorf("resolve path: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return promptAttachment{}, fmt.Errorf("read %s: %w", path, err)
	}
	if info.IsDir() {
		return promptAttachment{}, fmt.Errorf("directories are not supported: %s", path)
	}
	ext := strings.ToLower(filepath.Ext(abs))
	mimeType := mime.TypeByExtension(ext)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	if strings.HasPrefix(mimeType, "image/") {
		data, err := os.ReadFile(abs) //nolint:gosec // intentional: user explicitly typed this local attachment path
		if err != nil {
			return promptAttachment{}, fmt.Errorf("read %s: %w", path, err)
		}
		return promptAttachment{
			Path:     abs,
			Name:     filepath.Base(abs),
			Size:     info.Size(),
			MimeType: mimeType,
			Parts: []session.Part{{
				Kind:          session.PartImage,
				ImageMimeType: mimeType,
				ImageBase64:   base64.StdEncoding.EncodeToString(data),
			}},
		}, nil
	}
	if maxTextBytes > 0 && info.Size() > maxTextBytes {
		return promptAttachment{}, fmt.Errorf("file is too large (%s); text attachments are limited to %s", formatBytes(info.Size()), formatBytes(maxTextBytes))
	}
	data, err := os.ReadFile(abs) //nolint:gosec // intentional: user explicitly typed this local attachment path
	if err != nil {
		return promptAttachment{}, fmt.Errorf("read %s: %w", path, err)
	}
	if !utf8.Valid(data) {
		return promptAttachment{}, fmt.Errorf("binary files are not supported unless they are images")
	}
	text := fmt.Sprintf("Attached file: %s\n\n```\n%s\n```", abs, string(data))
	return promptAttachment{
		Path:     abs,
		Name:     filepath.Base(abs),
		Size:     info.Size(),
		MimeType: "text/plain; charset=utf-8",
		Parts:    []session.Part{{Kind: session.PartText, Text: text}},
	}, nil
}

func formatBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
}

func attachmentParts(text string, attachments []promptAttachment) []session.Part {
	parts := []session.Part{{Kind: session.PartText, Text: text}}
	for _, att := range attachments {
		parts = append(parts, att.Parts...)
	}
	return parts
}

func appendUniquePromptAttachments(dst []promptAttachment, src ...promptAttachment) []promptAttachment {
	seen := make(map[string]struct{}, len(dst)+len(src))
	for _, att := range dst {
		if att.Path != "" {
			seen[att.Path] = struct{}{}
		}
	}
	for _, att := range src {
		if att.Path != "" {
			if _, ok := seen[att.Path]; ok {
				continue
			}
			seen[att.Path] = struct{}{}
		}
		dst = append(dst, att)
	}
	return dst
}
