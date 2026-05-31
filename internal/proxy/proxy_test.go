package proxy

import (
	"net/http"
	"testing"

	"github.com/kyuyeonpark/reflet/internal/storage"
)

func TestReplaceRefsSubstitutesSecrets(t *testing.T) {
	server := &Server{store: newTestStore(t)}

	if err := server.store.Set("token", "secret-token"); err != nil {
		t.Fatalf("Set(token) error = %v", err)
	}
	if err := server.store.Set("org", "acme"); err != nil {
		t.Fatalf("Set(org) error = %v", err)
	}

	got, count, err := server.replaceRefs("Bearer ref://token for ref://org")
	if err != nil {
		t.Fatalf("replaceRefs() error = %v", err)
	}
	if got != "Bearer secret-token for acme" {
		t.Fatalf("replaceRefs() = %q, want %q", got, "Bearer secret-token for acme")
	}
	if count != 2 {
		t.Fatalf("replaceRefs() count = %d, want %d", count, 2)
	}
}

func TestRewriteAuthorizationHandlesMultipleHeaders(t *testing.T) {
	server := &Server{store: newTestStore(t)}

	if err := server.store.Set("alpha", "token-a"); err != nil {
		t.Fatalf("Set(alpha) error = %v", err)
	}
	if err := server.store.Set("beta", "token-b"); err != nil {
		t.Fatalf("Set(beta) error = %v", err)
	}

	header := http.Header{}
	header.Add("Authorization", "Bearer ref://alpha")
	header.Add("Authorization", "Basic ref://beta")

	count, err := server.rewriteAuthorization(header)
	if err != nil {
		t.Fatalf("rewriteAuthorization() error = %v", err)
	}
	if count != 2 {
		t.Fatalf("rewriteAuthorization() count = %d, want %d", count, 2)
	}

	got := header.Values("Authorization")
	want := []string{"Bearer token-a", "Basic token-b"}
	if len(got) != len(want) {
		t.Fatalf("Authorization header count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Authorization[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func newTestStore(t *testing.T) *storage.Store {
	t.Helper()

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	store, err := storage.NewStore("reflet-proxy-test")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	return store
}
