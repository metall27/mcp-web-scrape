package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

func main() {
	fmt.Printf("🔍 Анализ потери информации при оптимизации\n\n")

	// Get full releases data from GitHub API
	url := "https://api.github.com/repos/open-webui/open-webui/releases?per_page=30"

	client := &http.Client{}
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("❌ Error: %v\n", err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var releases []map[string]interface{}
	json.Unmarshal(body, &releases)

	fmt.Printf("📊 Полная статистика GitHub releases:\n")
	fmt.Printf("────────────────────────────────────────────\n")
	fmt.Printf("Всего releases получено: %d\n", len(releases))

	// Analyze what we're losing
	totalChars := 0
	totalAssets := 0
	releasesWithNotes := 0
	var versions []string

	for i, release := range releases {
		if name, ok := release["tag_name"].(string); ok {
			versions = append(versions, name)
		}

		if body, ok := release["body"].(string); ok {
			totalChars += len(body)
			if len(body) > 0 {
				releasesWithNotes++
			}
		}

		if assets, ok := release["assets"].([]interface{}); ok {
			totalAssets += len(assets)
		}

		// Show first few releases in detail
		if i < 3 {
			fmt.Printf("\nRelease #%d: %s\n", i+1, release["name"])
			if tag, ok := release["tag_name"].(string); ok {
				fmt.Printf("  Tag: %s\n", tag)
			}
			if date, ok := release["published_at"].(string); ok {
				fmt.Printf("  Date: %s\n", date[:10])
			}
			if body, ok := release["body"].(string); ok {
				fmt.Printf("  Notes length: %d chars\n", len(body))
				if len(body) > 0 {
					fmt.Printf("  Preview: %.100s...\n", body)
				}
			}
			if assets, ok := release["assets"].([]interface{}); ok {
				fmt.Printf("  Assets: %d files\n", len(assets))
				for j, asset := range assets {
					if assetMap, ok := asset.(map[string]interface{}); ok && j < 2 {
						if name, ok := assetMap["name"].(string); ok {
							fmt.Printf("    - %s\n", name)
						}
					}
				}
			}
		}
	}

	fmt.Printf("\n────────────────────────────────────────────\n")
	fmt.Printf("📈 Статистика по всем %d releases:\n", len(releases))
	fmt.Printf("Всего символов в release notes: %d (%.1f KB)\n", totalChars, float64(totalChars)/1024)
	fmt.Printf("Releases с заметками: %d/%d (%.1f%%)\n", releasesWithNotes, len(releases), float64(releasesWithNotes)*100/float64(len(releases)))
	fmt.Printf("Всего ассетов (файлов): %d\n", totalAssets)
	fmt.Printf("Средняя длина release notes: %.0f символов\n", float64(totalChars)/float64(len(releases)))

	// Show what user gets with current optimization
	fmt.Printf("\n⚠️  Что теряет пользователь при текущей оптимизации:\n")
	fmt.Printf("────────────────────────────────────────────\n")
	fmt.Printf("❌ Releases 6-%d (исторические версии)\n", len(releases))
	fmt.Printf("❌ Детальные release notes (ограничены до 500 символов)\n")
	fmt.Printf("❌ Полные списки изменений (changelogs)\n")

	// Calculate token impact
	fullTokens := totalChars / 4
	optimizedTokens := 740 // Current optimization
	fmt.Printf("\n🤖 Токены если использовать ВСЕ releases:\n")
	fmt.Printf("────────────────────────────────────────────\n")
	fmt.Printf("Текущая оптимизация: ~%d токенов (5 releases)\n", optimizedTokens)
	fmt.Printf("Все releases: ~%d токенов (%d releases)\n", fullTokens, len(releases))
	fmt.Printf("Потеря информации: %.1f%%\n", float64(fullTokens-optimizedTokens)*100/float64(fullTokens))
	fmt.Printf("Экономия токенов: %.1f%%\n", float64(fullTokens-optimizedTokens)*100/float64(fullTokens))

	fmt.Printf("\n💡 Выводы:\n")
	fmt.Printf("────────────────────────────────────────────\n")
	if fullTokens > 8000 {
		fmt.Printf("❌ Все releases занимают СЛИШКОМ МНОГО токенов (%d)\n", fullTokens)
		fmt.Printf("   Для эффективной работы с LLM нужна оптимизация\n\n")
		fmt.Printf("✅ Рекомендуется: Гибкая настройка\n")
		fmt.Printf("   - По умолчанию: 5 releases (текущая оптимизация)\n")
		fmt.Printf("   - Опционально: 10-20 releases для исследований\n")
		fmt.Printf("   - Полные releases только при необходимости\n")
	} else {
		fmt.Printf("✅ Можно использовать больше releases!\n")
	}
}