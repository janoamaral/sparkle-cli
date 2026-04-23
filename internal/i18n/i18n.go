package i18n

import (
	"os"
	"strings"
)

// Language represents the supported languages
type Language string

const (
	English Language = "en"
	Spanish Language = "es"
)

// Localizer provides localized strings based on the current language
type Localizer struct {
	lang Language
}

// New creates a new Localizer by detecting the system language
func New() *Localizer {
	return &Localizer{
		lang: DetectLanguage(),
	}
}

// NewWithLanguage creates a new Localizer with a specific language
func NewWithLanguage(lang Language) *Localizer {
	return &Localizer{
		lang: lang,
	}
}

// DetectLanguage detects the system language from environment variables
func DetectLanguage() Language {
	// Check LANG environment variable
	lang := os.Getenv("LANG")
	if lang != "" {
		if strings.HasPrefix(lang, "es") {
			return Spanish
		}
		if strings.HasPrefix(lang, "en") {
			return English
		}
	}

	// Check LC_ALL environment variable
	lcAll := os.Getenv("LC_ALL")
	if lcAll != "" {
		if strings.HasPrefix(lcAll, "es") {
			return Spanish
		}
		if strings.HasPrefix(lcAll, "en") {
			return English
		}
	}

	// Check LC_MESSAGES environment variable
	lcMessages := os.Getenv("LC_MESSAGES")
	if lcMessages != "" {
		if strings.HasPrefix(lcMessages, "es") {
			return Spanish
		}
		if strings.HasPrefix(lcMessages, "en") {
			return English
		}
	}

	// Default to English
	return English
}

// Get returns the localized string for the given key
func (l *Localizer) Get(key string) string {
	switch l.lang {
	case Spanish:
		if str, exists := spanishTranslations[key]; exists {
			return str
		}
	case English:
		if str, exists := englishTranslations[key]; exists {
			return str
		}
	}
	// Fallback to English
	if str, exists := englishTranslations[key]; exists {
		return str
	}
	// Return key itself if not found
	return key
}

// Lang returns the current language
func (l *Localizer) Lang() Language {
	return l.lang
}

// SetLanguage sets the language for the localizer
func (l *Localizer) SetLanguage(lang Language) {
	l.lang = lang
}
