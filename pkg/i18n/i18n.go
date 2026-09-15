package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
)

//go:embed locales/*.json
var LocalesFS embed.FS

// Manager manages loaded localization dictionaries and validation.
type Manager struct {
	mu        sync.RWMutex
	baseLang  string
	rawJSON   map[string][]byte
	flattened map[string]map[string]string
}

// NewManager initializes and loads all embedded translation files from locales/*.json.
func NewManager(baseLang string) (*Manager, error) {
	if baseLang == "" {
		baseLang = "ru"
	}

	m := &Manager{
		baseLang:  baseLang,
		rawJSON:   make(map[string][]byte),
		flattened: make(map[string]map[string]string),
	}

	if err := m.reload(); err != nil {
		return nil, err
	}

	return m, nil
}

func (m *Manager) reload() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	entries, err := LocalesFS.ReadDir("locales")
	if err != nil {
		return fmt.Errorf("failed to read locales directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		lang := strings.TrimSuffix(entry.Name(), ".json")
		content, err := LocalesFS.ReadFile("locales/" + entry.Name())
		if err != nil {
			return fmt.Errorf("failed to read locale file %s: %w", entry.Name(), err)
		}

		var parsed map[string]interface{}
		if err := json.Unmarshal(content, &parsed); err != nil {
			return fmt.Errorf("invalid JSON syntax in %s: %w", entry.Name(), err)
		}

		flat := make(map[string]string)
		flattenMap("", parsed, flat)

		m.rawJSON[lang] = content
		m.flattened[lang] = flat
	}

	if len(m.flattened) == 0 {
		return fmt.Errorf("no translation files found in locales/")
	}

	if _, ok := m.flattened[m.baseLang]; !ok {
		return fmt.Errorf("base language %q not found among loaded translations", m.baseLang)
	}

	return nil
}

func flattenMap(prefix string, in map[string]interface{}, out map[string]string) {
	for k, v := range in {
		fullKey := k
		if prefix != "" {
			fullKey = prefix + "." + k
		}
		switch val := v.(type) {
		case map[string]interface{}:
			flattenMap(fullKey, val, out)
		case string:
			out[fullKey] = val
		default:
			out[fullKey] = fmt.Sprintf("%v", val)
		}
	}
}

// Languages returns a sorted slice of available language codes.
func (m *Manager) Languages() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	langs := make([]string, 0, len(m.flattened))
	for l := range m.flattened {
		langs = append(langs, l)
	}
	sort.Strings(langs)
	return langs
}

// BaseLanguage returns the configured primary base language code.
func (m *Manager) BaseLanguage() string {
	return m.baseLang
}

// Get translates a dot-notation key for the given language with fallback to base language.
func (m *Manager) Get(lang, key string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if dict, ok := m.flattened[lang]; ok {
		if val, ok := dict[key]; ok && val != "" {
			return val
		}
	}

	if baseDict, ok := m.flattened[m.baseLang]; ok {
		if val, ok := baseDict[key]; ok {
			return val
		}
	}

	return key
}

// RawLocaleJSON returns the raw JSON content of the requested language file.
func (m *Manager) RawLocaleJSON(lang string) ([]byte, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	data, ok := m.rawJSON[lang]
	return data, ok
}

// ValidateSync performs a strict mutual synchronization check between all translation files.
// It verifies:
// 1. Every key in baseLang exists in target language (no missing translations).
// 2. No target language has extraneous keys not present in baseLang (no orphaned keys).
// 3. No translation has an empty string value.
func (m *Manager) ValidateSync() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var errors []string

	baseDict, ok := m.flattened[m.baseLang]
	if !ok {
		return []string{fmt.Sprintf("base language %q is not loaded", m.baseLang)}
	}

	// Collect and sort all base keys
	baseKeys := make([]string, 0, len(baseDict))
	for k, v := range baseDict {
		if strings.TrimSpace(v) == "" {
			errors = append(errors, fmt.Sprintf("[%s] key %q has empty translation value in base language", m.baseLang, k))
		}
		baseKeys = append(baseKeys, k)
	}
	sort.Strings(baseKeys)

	// Check each target locale against base
	for lang, targetDict := range m.flattened {
		if lang == m.baseLang {
			continue
		}

		// 1. Check for missing or empty keys
		for _, k := range baseKeys {
			val, exists := targetDict[k]
			if !exists {
				errors = append(errors, fmt.Sprintf("[%s] missing translation for key: %q (present in base %s)", lang, k, m.baseLang))
			} else if strings.TrimSpace(val) == "" {
				errors = append(errors, fmt.Sprintf("[%s] empty translation value for key: %q", lang, k))
			}
		}

		// 2. Check for extraneous keys in target not present in base
		for k := range targetDict {
			if _, exists := baseDict[k]; !exists {
				errors = append(errors, fmt.Sprintf("[%s] extraneous key %q not found in base %s", lang, k, m.baseLang))
			}
		}
	}

	return errors
}

// RegisterRoutes registers HTTP endpoints for localization under basePath (e.g. "/api/i18n").
// Endpoints:
//   GET <basePath>/languages      -> JSON array of language codes ["en", "ru"]
//   GET <basePath>/locales/{lang} -> JSON content of locales/{lang}.json
//   GET <basePath>/sync           -> JSON status of translation synchronization check
func (m *Manager) RegisterRoutes(mux *http.ServeMux, basePath string) {
	basePath = strings.TrimSuffix(basePath, "/")

	mux.HandleFunc(basePath+"/languages", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"base":      m.baseLang,
			"languages": m.Languages(),
		})
	})

	mux.HandleFunc(basePath+"/sync", func(w http.ResponseWriter, r *http.Request) {
		errors := m.ValidateSync()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		status := "ok"
		if len(errors) > 0 {
			status = "desynchronized"
			w.WriteHeader(http.StatusConflict)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": status,
			"errors": errors,
		})
	})

	mux.HandleFunc(basePath+"/locales/", func(w http.ResponseWriter, r *http.Request) {
		lang := strings.TrimPrefix(r.URL.Path, basePath+"/locales/")
		lang = strings.TrimSuffix(lang, ".json")

		data, ok := m.RawLocaleJSON(lang)
		if !ok {
			http.Error(w, fmt.Sprintf("locale %q not found", lang), http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	})
}
