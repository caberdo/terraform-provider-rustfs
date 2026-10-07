package client_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/weinmann-emt/terraform-provider-rustfs/internal/client"
)

// newTestAdminServer spins up an httptest server speaking the RustFS admin
// API and returns a RustfsAdmin client pointed at it.
func newTestAdminServer(t *testing.T, handler http.HandlerFunc) *client.RustfsAdmin {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := client.New(&client.RustfsAdminConfig{
		AccessKey:    "admin",
		AccessSecret: "secret",
		Endpoint:     strings.TrimPrefix(srv.URL, "http://"),
	})
	return &c
}

func TestIsAdmin(t *testing.T) {
	endpoint := os.Getenv("RUSTFS_ENDPOINT")
	key := os.Getenv("RUSTFS_USER")
	secret := os.Getenv("RUSTFS_SECRET")

	config := client.RustfsAdminConfig{
		AccessKey:    key,
		AccessSecret: secret,
		Endpoint:     endpoint,

		Ssl: false,
	}

	dut := client.New(&config)
	admin, _ := dut.IsAdmin()
	if !admin {
		t.Error("User is no admin")
	}

}
