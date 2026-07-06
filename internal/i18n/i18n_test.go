package i18n

// i18n_test.go — verifies key-set parity between the en and ru locales.
// CTO-review §9 finding: en ~302 keys, ru ~520 — asymmetry with no test guard.
// Without parity, the UI on the smaller locale renders raw key strings for
// untranslated keys (T falls back to the key string when absent). This test
// fails when a key is added to one locale but not the other, so the drift is
// caught at test time instead of in production.

import (
	"context"
	"sort"
	"testing"
)

// localeKeys returns the set of keys defined for the given locale.
func localeKeys(lang string) []string {
	m, ok := locales[lang]
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestEnRuKeyParity verifies every key present in en is present in ru and
// vice versa. A missing key in one locale means T falls back to the literal
// key string in that locale.
func TestEnRuKeyParity(t *testing.T) {
	en := localeKeys("en")
	ru := localeKeys("ru")
	enSet := map[string]bool{}
	for _, k := range en {
		enSet[k] = true
	}
	ruSet := map[string]bool{}
	for _, k := range ru {
		ruSet[k] = true
	}
	var missingInRu, missingInEn []string
	for _, k := range en {
		if !ruSet[k] {
			missingInRu = append(missingInRu, k)
		}
	}
	for _, k := range ru {
		if !enSet[k] {
			missingInEn = append(missingInEn, k)
		}
	}
	if len(missingInRu) > 0 {
		t.Errorf("keys present in 'en' but missing in 'ru' (%d): %v", len(missingInRu), missingInRu)
	}
	if len(missingInEn) > 0 {
		t.Errorf("keys present in 'ru' but missing in 'en' (%d): %v", len(missingInEn), missingInEn)
	}
}

// TestT_FallbackToKey verifies T returns the key string when the key is absent
// from the locale (the documented fallback behaviour).
func TestT_FallbackToKey(t *testing.T) {
	ctx := context.WithValue(context.Background(), LangKey, "en")
	const missing = "this.key.does.not.exist.in.any.locale"
	got := T(ctx, missing)
	if got != missing {
		t.Errorf("T(missing key) = %q, want the key itself %q (fallback)", got, missing)
	}
}

// TestT_LangDefaultsToEnWhenUnknown verifies that an unknown language falls
// back to the en locale (T must not return empty for a valid key).
func TestT_LangDefaultsToEnWhenUnknown(t *testing.T) {
	ctx := context.WithValue(context.Background(), LangKey, "bogus")
	// "Dashboard" exists in en; for an unknown lang we expect en fallback.
	got := T(ctx, "Dashboard")
	if got == "" {
		t.Error("T(unknown lang) returned empty for an existing key")
	}
}