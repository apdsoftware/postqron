package publishingruntime

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type fixedNetworkResolver struct {
	addresses []netip.Addr
	err       error
}

func (resolver fixedNetworkResolver) LookupNetIP(
	context.Context,
	string,
	string,
) ([]netip.Addr, error) {
	return append([]netip.Addr(nil), resolver.addresses...), resolver.err
}

func TestF5ExecutorBootstrapRequiresCompleteGatedConfiguration(t *testing.T) {
	for _, key := range []string{
		"POSTQRON_F08_X_ENABLED",
		"POSTQRON_F08_X_REVIEW_APPROVED",
		"POSTQRON_F08_X_RUNTIME_AUDIT_VERIFIED",
		"POSTQRON_F08_X_QUOTA_CONFIGURED",
		"POSTQRON_F05_ENABLED",
		"POSTQRON_F05_CIPHER_KEY_ID",
		"POSTQRON_F05_CIPHER_KEY_BASE64",
		"POSTQRON_F05_X_RESOURCE_SERVER",
	} {
		t.Setenv(key, "")
	}
	database, err := sql.Open(
		"pgx",
		"postgres://worker:worker@127.0.0.1/postqron",
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	executor, err := NewF5AuthenticatedExecutor(database, nil)
	if err != nil || executor != nil {
		t.Fatalf("ungated executor=%v err=%v", executor, err)
	}
	for _, key := range []string{
		"POSTQRON_F08_X_ENABLED",
		"POSTQRON_F08_X_REVIEW_APPROVED",
		"POSTQRON_F08_X_RUNTIME_AUDIT_VERIFIED",
		"POSTQRON_F08_X_QUOTA_CONFIGURED",
	} {
		t.Setenv(key, "true")
	}
	if _, err = NewF5AuthenticatedExecutor(database, nil); err == nil {
		t.Fatal("ready F8 gate accepted missing F5 boundary configuration")
	}
	t.Setenv("POSTQRON_F05_ENABLED", "true")
	t.Setenv("POSTQRON_F05_CIPHER_KEY_ID", "worker-test-key")
	t.Setenv(
		"POSTQRON_F05_CIPHER_KEY_BASE64",
		base64.StdEncoding.EncodeToString(
			[]byte("0123456789abcdef0123456789abcdef"),
		),
	)
	t.Setenv("POSTQRON_F05_X_RESOURCE_SERVER", "https://attacker.example")
	if _, err = NewF5AuthenticatedExecutor(database, nil); err == nil {
		t.Fatal("non-official resource server was accepted")
	}
	t.Setenv("POSTQRON_F05_X_RESOURCE_SERVER", "https://api.x.com")
	executor, err = NewF5AuthenticatedExecutor(database, nil)
	if err != nil || executor == nil {
		t.Fatalf("configured executor=%v err=%v", executor, err)
	}
}

func TestDNSPinnedTransportRejectsPrivateAndUnpinnedOrigins(t *testing.T) {
	private := newDNSPinnedTransport(fixedNetworkResolver{
		addresses: []netip.Addr{netip.MustParseAddr("127.0.0.1")},
	})
	if err := private.PinOrigin(
		context.Background(),
		"https://api.x.com",
	); err == nil {
		t.Fatal("private DNS result was accepted")
	}
	public := newDNSPinnedTransport(fixedNetworkResolver{
		addresses: []netip.Addr{netip.MustParseAddr("8.8.8.8")},
	})
	if err := public.PinOrigin(
		context.Background(),
		"https://api.x.com?redirect=1",
	); err == nil {
		t.Fatal("origin with query was accepted")
	}
	request, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"https://api.x.com/2/users/1/tweets",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = public.RoundTrip(request); err == nil ||
		!errors.Is(err, errOriginNotPinned) {
		t.Fatalf("unpinned origin error=%v", err)
	}
	if err = public.PinOrigin(
		context.Background(),
		"https://api.x.com",
	); err != nil {
		t.Fatal(err)
	}
	dialAddress := ""
	public.dialContext = func(
		_ context.Context,
		_ string,
		address string,
	) (net.Conn, error) {
		dialAddress = address
		return nil, errors.New("fixture dial stopped")
	}
	if _, err = public.RoundTrip(request); err == nil {
		t.Fatal("fixture dial unexpectedly succeeded")
	}
	if dialAddress != "8.8.8.8:443" {
		t.Fatalf("dial address=%q", dialAddress)
	}
}
