package llm

import "testing"

// Independence is stored per build, so its classification has to be right: a
// wrong label would understate collusion risk in exactly the case that matters.
func TestIndependenceClassification(t *testing.T) {
	for _, testCase := range []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "different endpoints are cross provider",
			cfg: Config{
				Compiler: RoleConfig{BaseURL: "https://a.example/v1", APIKey: "k", Model: "m1"},
				Reviewer: RoleConfig{BaseURL: "https://b.example/v1", APIKey: "k", Model: "m2"},
			},
			want: IndependenceCrossProvider,
		},
		{
			name: "same endpoint different model",
			cfg: Config{
				Compiler: RoleConfig{BaseURL: "https://a.example/v1", APIKey: "k", Model: "qwen3.7-plus"},
				Reviewer: RoleConfig{BaseURL: "https://a.example/v1", APIKey: "k", Model: "qwen-max"},
			},
			want: IndependenceSameProvider,
		},
		{
			name: "identical model is self review",
			cfg: Config{
				Compiler: RoleConfig{BaseURL: "https://a.example/v1", APIKey: "k", Model: "same"},
				Reviewer: RoleConfig{BaseURL: "https://a.example/v1", APIKey: "k", Model: "SAME"},
			},
			want: IndependenceSameModel,
		},
		{
			name: "missing reviewer key",
			cfg: Config{
				Compiler: RoleConfig{BaseURL: "https://a.example/v1", APIKey: "k", Model: "m"},
				Reviewer: RoleConfig{BaseURL: "https://a.example/v1", Model: "m2"},
			},
			want: IndependenceUnavailable,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := testCase.cfg.Independence(); got != testCase.want {
				t.Fatalf("Independence() = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestRoleConfiguredRequiresAllThree(t *testing.T) {
	full := RoleConfig{BaseURL: "https://a.example/v1", APIKey: "k", Model: "m"}
	if !full.Configured() {
		t.Fatal("a complete role should be configured")
	}
	for _, incomplete := range []RoleConfig{
		{APIKey: "k", Model: "m"},
		{BaseURL: "https://a.example/v1", Model: "m"},
		{BaseURL: "https://a.example/v1", APIKey: "k"},
	} {
		if incomplete.Configured() {
			t.Fatalf("incomplete role reported configured: %+v", incomplete)
		}
	}
}

// clearRoleOverrides blanks every role-specific override that ConfigFromEnv reads.
// The env helper treats an empty value as unset, and t.Setenv restores the original
// after the test — without this, a developer shell that exports REVIEWER_* (the
// documented way to get a cross-provider reviewer) leaks into the test and flips
// the expected independence classification.
func clearRoleOverrides(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"COMPILER_BASE_URL", "COMPILER_API_KEY", "COMPILER_MODEL",
		"REVIEWER_BASE_URL", "REVIEWER_API_KEY", "REVIEWER_MODEL",
	} {
		t.Setenv(key, "")
	}
}

// The default deployment has one credential, so both roles fall back to the
// embedding endpoint. That must still yield a working compiler and an honest
// same_provider label rather than a silently self-reviewing setup.
func TestConfigFromEnvFallsBackToSharedCredential(t *testing.T) {
	clearRoleOverrides(t)
	t.Setenv("EMBEDDING_BASE_URL", "https://dashscope.example/compatible-mode/v1")
	t.Setenv("EMBEDDING_API_KEY", "shared-key")

	cfg := ConfigFromEnv()
	if !cfg.Compiler.Configured() || !cfg.Reviewer.Configured() {
		t.Fatalf("both roles should be usable: %+v", cfg)
	}
	if cfg.Compiler.Model == cfg.Reviewer.Model {
		t.Fatal("the default reviewer model must differ from the compiler's")
	}
	if got := cfg.Independence(); got != IndependenceSameProvider {
		t.Fatalf("Independence() = %q, want %q", got, IndependenceSameProvider)
	}
}

func TestConfigFromEnvRoleOverridesWin(t *testing.T) {
	clearRoleOverrides(t)
	t.Setenv("EMBEDDING_BASE_URL", "https://shared.example/v1")
	t.Setenv("EMBEDDING_API_KEY", "shared-key")
	t.Setenv("REVIEWER_BASE_URL", "https://other-vendor.example/v1")
	t.Setenv("REVIEWER_API_KEY", "reviewer-key")
	t.Setenv("REVIEWER_MODEL", "some-other-model")

	cfg := ConfigFromEnv()
	if cfg.Reviewer.BaseURL != "https://other-vendor.example/v1" {
		t.Fatalf("reviewer base url = %q", cfg.Reviewer.BaseURL)
	}
	if got := cfg.Independence(); got != IndependenceCrossProvider {
		t.Fatalf("Independence() = %q, want %q", got, IndependenceCrossProvider)
	}
}

// Compilation should be as reproducible as an LLM allows, and judgement should be
// stable: the same claim against the same source ought to get the same verdict.
func TestDefaultTemperaturesFavourReproducibility(t *testing.T) {
	t.Setenv("EMBEDDING_API_KEY", "shared-key")
	cfg := ConfigFromEnv()
	if cfg.Compiler.Temperature > 0.3 {
		t.Fatalf("compiler temperature = %v, expected low", cfg.Compiler.Temperature)
	}
	if cfg.Reviewer.Temperature != 0 {
		t.Fatalf("reviewer temperature = %v, want 0", cfg.Reviewer.Temperature)
	}
}
