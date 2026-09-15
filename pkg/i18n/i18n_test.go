package i18n

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestI18n_SyncValidation(t *testing.T) {
	mgr, err := NewManager("ru")
	if err != nil {
		t.Fatalf("failed to initialize i18n manager: %v", err)
	}

	syncErrors := mgr.ValidateSync()
	if len(syncErrors) > 0 {
		t.Fatalf("Translation files are out of sync:\n%s", formatErrors(syncErrors))
	}
}

func TestI18n_DetectDesynchronization(t *testing.T) {
	mgr, err := NewManager("ru")
	if err != nil {
		t.Fatalf("failed to initialize i18n manager: %v", err)
	}

	// Artificially inject a missing key in 'en'
	delete(mgr.flattened["en"], "app.subtitle")
	syncErrors := mgr.ValidateSync()
	if len(syncErrors) == 0 {
		t.Fatal("expected ValidateSync to report missing key 'app.subtitle', but got 0 errors")
	}

	// Restore and inject an extraneous key in 'en'
	mgr.reload()
	mgr.flattened["en"]["bogus.extra.key"] = "test"
	syncErrors = mgr.ValidateSync()
	if len(syncErrors) == 0 {
		t.Fatal("expected ValidateSync to report extraneous key, but got 0 errors")
	}

	// Restore and inject an empty key in 'en'
	mgr.reload()
	mgr.flattened["en"]["app.subtitle"] = ""
	syncErrors = mgr.ValidateSync()
	if len(syncErrors) == 0 {
		t.Fatal("expected ValidateSync to report empty value, but got 0 errors")
	}
}

func TestI18n_GetAndFallback(t *testing.T) {
	mgr, err := NewManager("ru")
	if err != nil {
		t.Fatalf("failed to initialize i18n manager: %v", err)
	}

	// Russian (base)
	if val := mgr.Get("ru", "app.title"); val != "Gyro Bridge" {
		t.Errorf("expected 'Gyro Bridge', got %q", val)
	}
	if val := mgr.Get("ru", "status.online"); val != "online" {
		t.Errorf("expected 'online', got %q", val)
	}

	// English
	if val := mgr.Get("en", "status.online"); val != "online" {
		t.Errorf("expected 'online', got %q", val)
	}

	// Fallback to base on unknown lang
	if val := mgr.Get("fr", "controls.pause"); val != "ПАУЗА" {
		t.Errorf("expected fallback to Russian 'ПАУЗА', got %q", val)
	}

	// Missing key returns key name
	if val := mgr.Get("ru", "nonexistent.key"); val != "nonexistent.key" {
		t.Errorf("expected 'nonexistent.key', got %q", val)
	}
}

func TestI18n_HTTPEndpoints(t *testing.T) {
	mgr, err := NewManager("ru")
	if err != nil {
		t.Fatalf("failed to initialize i18n manager: %v", err)
	}

	mux := http.NewServeMux()
	mgr.RegisterRoutes(mux, "/api/i18n")

	// 1. /api/i18n/languages
	req := httptest.NewRequest("GET", "/api/i18n/languages", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	// 2. /api/i18n/sync
	req = httptest.NewRequest("GET", "/api/i18n/sync", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	// 3. /api/i18n/locales/ru
	req = httptest.NewRequest("GET", "/api/i18n/locales/ru", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	// 4. Unknown locale -> 404
	req = httptest.NewRequest("GET", "/api/i18n/locales/de", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func formatErrors(errs []string) string {
	res := ""
	for _, e := range errs {
		res += "  - " + e + "\n"
	}
	return res
}
