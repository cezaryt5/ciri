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

// AllCategories returns the five top-level categories in display order.
func AllCategories() []Category {
	return []Category{
		CategoryCoding,
		CategoryChat,
		CategoryGeneral,
		CategoryVision,
		CategoryTranslation,
	}
}

// Categorize populates m.Categories based on the model's UseCase,
// Capabilities and PipelineTag. A model may belong to more than one
// category (e.g. a vision-capable chat model).
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

func hasCapability(m *Model, want string) bool {
	for _, c := range m.Capabilities {
		if strings.EqualFold(c, want) {
			return true
		}
	}
	return false
}
