package runner

import "testing"

func TestEnvPairsSetValidatesRefPrefix(t *testing.T) {
	var pairs EnvPairs

	if err := pairs.Set("OPENAI_API_KEY=value"); err == nil {
		t.Fatal("Set() error = nil for non-ref value, want non-nil")
	}

	if err := pairs.Set("OPENAI_API_KEY=ref://openai-api-key"); err != nil {
		t.Fatalf("Set() valid ref error = %v", err)
	}
	if len(pairs) != 1 {
		t.Fatalf("len(EnvPairs) = %d, want %d", len(pairs), 1)
	}
	if got := pairs[0]; got.Name != "OPENAI_API_KEY" || got.Value != "ref://openai-api-key" {
		t.Fatalf("EnvPairs[0] = %+v, want Name=%q Value=%q", got, "OPENAI_API_KEY", "ref://openai-api-key")
	}
}

func TestBuildEnvSetsProxyAndRefletEnabled(t *testing.T) {
	env := buildEnv(
		[]string{"PATH=/usr/bin", "HTTP_PROXY=http://old-proxy:8080"},
		[]EnvPair{{Name: "OPENAI_API_KEY", Value: "ref://openai-api-key"}},
		"127.0.0.1:7777",
		"/tmp/reflet-ca.pem",
	)

	values := envMap(env)
	if values["HTTP_PROXY"] != "http://127.0.0.1:7777" {
		t.Fatalf("HTTP_PROXY = %q, want %q", values["HTTP_PROXY"], "http://127.0.0.1:7777")
	}
	if values["REFLET_ENABLED"] != "true" {
		t.Fatalf("REFLET_ENABLED = %q, want %q", values["REFLET_ENABLED"], "true")
	}
	if values["OPENAI_API_KEY"] != "ref://openai-api-key" {
		t.Fatalf("OPENAI_API_KEY = %q, want %q", values["OPENAI_API_KEY"], "ref://openai-api-key")
	}
}

func envMap(items []string) map[string]string {
	values := make(map[string]string, len(items))
	for _, item := range items {
		name, value, ok := splitEnv(item)
		if ok {
			values[name] = value
		}
	}
	return values
}

func splitEnv(item string) (string, string, bool) {
	for i := 0; i < len(item); i++ {
		if item[i] == '=' {
			return item[:i], item[i+1:], true
		}
	}
	return "", "", false
}
