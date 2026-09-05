package antigravity

import "testing"

func TestIsAntigravityOnlyGeminiModel(t *testing.T) {
	t.Parallel()

	antigravityOnly := []string{
		"gemini-3.6-flash",
		"gemini-3.6-flash-high",
		"gemini-3.6-flash-low",
		"gemini-3.6-flash-medium",
		"gemini-3.6-flash-tiered",
		"gemini-3.7-flash",
		"gemini-3.7-flash-high",
		"gemini-3.7-flash-low",
		"gemini-3.7-flash-medium",
		"gemini-3.7-flash-tiered",
		"gemini-3.8-flash",
		"gemini-3.8-flash-high",
		"gemini-3.8-flash-low",
		"gemini-3.8-flash-medium",
		"gemini-3.8-flash-tiered",
	}
	for _, id := range antigravityOnly {
		if !IsAntigravityOnlyGeminiModel(id) {
			t.Fatalf("expected %q to be recognized as Antigravity-only", id)
		}
		// The Gemini native path prefixes model IDs with "models/".
		if !IsAntigravityOnlyGeminiModel("models/" + id) {
			t.Fatalf("expected models/%s to be recognized as Antigravity-only", id)
		}
		// Detection must be case-insensitive like DetectModelPlatform.
		if !IsAntigravityOnlyGeminiModel("Gemini-" + id[len("gemini-"):]) {
			t.Fatalf("expected case-insensitive match for %q", id)
		}
	}

	// Models served by the public Gemini channel must keep their platform.
	shared := []string{
		"gemini-2.5-flash",
		"gemini-2.5-pro",
		"gemini-3-flash",
		"gemini-3-flash-preview",
		"gemini-3-pro-high",
		"gemini-3-pro-preview",
		"gemini-3.1-pro-high",
		"gemini-3.1-flash-image",
		"gemini-3.5-flash",
		"gemini-3.6-pro",
		"gemini-3.8-pro",
		"gemini-3.8-flash-image",
		"",
	}
	for _, id := range shared {
		if IsAntigravityOnlyGeminiModel(id) {
			t.Fatalf("expected %q NOT to be treated as Antigravity-only", id)
		}
	}
}

func TestAntigravityOnlyGeminiModelsAreExposedInDefaultModels(t *testing.T) {
	t.Parallel()

	byID := make(map[string]struct{})
	for _, m := range DefaultModels() {
		byID[m.ID] = struct{}{}
	}

	ids := AntigravityOnlyGeminiModels()
	if len(ids) != 15 {
		t.Fatalf("expected 15 Antigravity-only Gemini models, got %d", len(ids))
	}
	for _, id := range ids {
		if _, ok := byID[id]; !ok {
			t.Fatalf("model %q is flagged Antigravity-only but missing from DefaultModels", id)
		}
	}
}
