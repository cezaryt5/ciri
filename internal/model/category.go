package model

import "strings"

// Category groups models by their primary use case for the TUI menu.
type Category string

const (
	CategoryCoding      Category = "Coding"
	CategoryChat        Category = "Chat"
	CategoryGeneral     Category = "General"
	CategoryVision      Category = "Vision"
	CategoryTranslation Category = "Translation"
)

// AllCategories — internal/model/category.go:17
// Called from: predictor.go:85 (in CountByCategory); home.go:25,29,45; model_test.go:197
// Returns the five top-level categories (Coding, Chat, General, Vision,
// Translation) in display order as used by the home screen menu.
func AllCategories() []Category {
	return []Category{
		CategoryCoding,
		CategoryChat,
		CategoryGeneral,
		CategoryVision,
		CategoryTranslation,
	}
}

// Categorize — internal/model/category.go:30
// Called from: catalog.go:47 (in LoadCatalog); model_test.go:109,117,131,139,153,167,175,183
// Populates m.Categories based on UseCase, Capabilities and PipelineTag.
// A model may belong to multiple categories (e.g. vision + chat).
// Falls back to CategoryGeneral if no specific category matches.
func Categorize(m *Model) {
	use := strings.ToLower(m.UseCase)
	pipeline := strings.ToLower(m.PipelineTag)

	if strings.Contains(use, "code") {
		m.Categories = append(m.Categories, CategoryCoding)
	}
	if strings.Contains(use, "chat") || strings.Contains(use, "instruction following") {
		m.Categories = append(m.Categories, CategoryChat)
	}
	if hasCapability(m, "vision") || pipeline == "image-text-to-text" ||
		strings.Contains(use, "multimodal") || strings.Contains(use, "vision") {
		m.Categories = append(m.Categories, CategoryVision)
	}
	if pipeline == "translation" || pipeline == "automatic-speech-recognition" {
		m.Categories = append(m.Categories, CategoryTranslation)
	}

	// General is a catch-all: everything that wasn't matched above, plus
	// all text-generation and unknown-pipeline models.
	if len(m.Categories) == 0 {
		m.Categories = append(m.Categories, CategoryGeneral)
	}
}

// hasCapability — internal/model/category.go:55
// Called from: category.go:40 (in Categorize)
// Checks whether a model's Capabilities list contains the given capability
// string (case-insensitive).
func hasCapability(m *Model, want string) bool {
	for _, c := range m.Capabilities {
		if strings.EqualFold(c, want) {
			return true
		}
	}
	return false
}
