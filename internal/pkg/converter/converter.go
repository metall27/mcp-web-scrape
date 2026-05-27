package converter

import (
	"fmt"
	"regexp"
	"strings"

	md "github.com/JohannesKaufmann/html-to-markdown"
)

type Format string

const (
	FormatHTML     Format = "html"
	FormatMarkdown Format = "markdown"
)

type Converter struct {
	mdConverter interface{}
}

func New() *Converter {
	// Создаем конвертер с базовыми настройками
	return &Converter{
		mdConverter: md.NewConverter("", true, nil),
	}
}

// Convert конвертирует HTML в указанный формат
func (c *Converter) Convert(html string, format Format) (string, error) {
	switch format {
	case FormatHTML:
		return html, nil
	case FormatMarkdown:
		return c.htmlToMarkdown(html)
	default:
		return "", fmt.Errorf("unsupported format: %s", format)
	}
}

// htmlToMarkdown конвертирует HTML в Markdown с оптимизацией
func (c *Converter) htmlToMarkdown(html string) (string, error) {
	// Используем библиотеку для базовой конвертации
	converter, ok := c.mdConverter.(interface{ ConvertString(string) (string, error) })
	if !ok {
		return "", fmt.Errorf("converter does not support ConvertString")
	}

	md, err := converter.ConvertString(html)
	if err != nil {
		return "", fmt.Errorf("markdown conversion failed: %w", err)
	}

	// Оптимизация Markdown
	md = c.optimizeMarkdown(md)

	return md, nil
}

// optimizeMarkdown очищает и оптимизирует Markdown
func (c *Converter) optimizeMarkdown(md string) string {
	// Удаляем лишние пустые строки (более 2 подряд)
	md = regexp.MustCompile(`\n{3,}`).ReplaceAllString(md, "\n\n")

	// Удаляем пробелы в конце строк
	lines := strings.Split(md, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	md = strings.Join(lines, "\n")

	// Удаляем leading/trailing whitespace
	md = strings.TrimSpace(md)

	// Удаляем слишком длинные последовательности дефисов (линии разделители)
	md = regexp.MustCompile(`-{20,}`).ReplaceAllString(md, "---")

	// Очищаем множественные пробелы внутри строк (но не в коде)
	lines = strings.Split(md, "\n")
	for i, line := range lines {
		// Не трогаем код блоки
		if !strings.HasPrefix(strings.TrimLeft(line, " \t"), "```") {
			// Заменяем множественные пробелы на один (но не в префиксе)
			if len(line) > 0 && line[0] != ' ' && line[0] != '\t' {
				line = regexp.MustCompile(`[ \t]{2,}`).ReplaceAllString(line, " ")
			}
		}
		lines[i] = line
	}
	md = strings.Join(lines, "\n")

	return md
}

// GetStats возвращает статистику о конвертации
type ConversionStats struct {
	OriginalSize int
	FinalSize    int
	Reduction    int
	ReductionPct float64
	Format       Format
}

// ConvertWithStats конвертирует и возвращает статистику
func (c *Converter) ConvertWithStats(html string, format Format) (string, *ConversionStats, error) {
	originalSize := len(html)

	result, err := c.Convert(html, format)
	if err != nil {
		return "", nil, err
	}

	finalSize := len(result)
	reduction := originalSize - finalSize
	reductionPct := 0.0
	if originalSize > 0 {
		reductionPct = float64(reduction) / float64(originalSize) * 100
	}

	stats := &ConversionStats{
		OriginalSize: originalSize,
		FinalSize:    finalSize,
		Reduction:    reduction,
		ReductionPct: reductionPct,
		Format:       format,
	}

	return result, stats, nil
}

// EstimateTokenRough оценка токенов для Markdown (грубо)
// Markdown обычно меньше чем HTML в 2-3 раза для LLM
func EstimateTokenRough(markdown string) int {
	// Грубая оценка: ~4 символа = 1 токен для английского
	// Для русского может быть меньше, но это approximation
	return len(markdown) / 4
}

// ShouldConvertToMarkdown определяет, стоит ли конвертировать в Markdown
// на основе размера HTML и типа контента
func ShouldConvertToMarkdown(html string, preferMarkdown bool) bool {
	// Если пользователь явно запросил Markdown
	if preferMarkdown {
		return true
	}

	// Для больших HTML файлов (>20KB) Markdown обычно эффективнее
	if len(html) > 20*1024 {
		return true
	}

	// Для маленьких файлов HTML может быть лучше (меньше overhead)
	return false
}

// StripHTML убирает все HTML теги, оставляя только текст
// Это fallback если markdown конвертация не удалась
func StripHTML(html string) string {
	// Удаляем скрипты и стили
	html = regexp.MustCompile(`<(script|style)[\s\S]*?</\1>`).ReplaceAllString(html, "")

	// Удаляем HTML теги
	html = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(html, "")

	// Декодируем базовые HTML entities
	html = strings.ReplaceAll(html, "&nbsp;", " ")
	html = strings.ReplaceAll(html, "&amp;", "&")
	html = strings.ReplaceAll(html, "&lt;", "<")
	html = strings.ReplaceAll(html, "&gt;", ">")
	html = strings.ReplaceAll(html, "&quot;", "\"")
	html = strings.ReplaceAll(html, "&#39;", "'")

	// Очищаем пробелы и переносы
	html = regexp.MustCompile(`\s+`).ReplaceAllString(html, " ")
	html = strings.TrimSpace(html)

	return html
}
