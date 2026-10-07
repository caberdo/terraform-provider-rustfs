package rustfs_test

import (
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/weinmann-emt/terraform-provider-rustfs/pkg/rustfs"
)

func getClient(t *testing.T) rustfs.RustfsAdmin {
	t.Helper()
	endpoint := os.Getenv("RUSTFS_ENDPOINT")
	key := os.Getenv("RUSTFS_USER")
	secret := os.Getenv("RUSTFS_SECRET")

	if endpoint == "" || key == "" || secret == "" {
		t.Skip("skipping live RustFS admin test: set RUSTFS_ENDPOINT, RUSTFS_USER and RUSTFS_SECRET to run")
	}

	config := rustfs.RustfsAdminConfig{
		AccessKey:    key,
		AccessSecret: secret,
		Endpoint:     endpoint,

		Ssl: false,
	}

	dut := rustfs.New(&config)
	return dut
}

func randomString(length int) string {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, length)

	for i := range result {
		result[i] = charset[rng.Intn(len(charset))]
	}

	return string(result)
}

func TestCreateServiceAccount(t *testing.T) {

	account := rustfs.ServiceAccount{
		AccessKey: randomString(8),
		SecretKey: "someSuperS3cret",
		Name:      randomString(8),
	}
	dut := getClient(t)
	err := dut.CreateServiceAccount(account)
	if err != nil {
		t.Error(err)
	}
}

func TestCreateAndDeleteServiceAccount(t *testing.T) {

	account := rustfs.ServiceAccount{
		AccessKey: randomString(8),
		SecretKey: "someSuperS3cret",
		Name:      randomString(8),
	}
	dut := getClient(t)
	err := dut.CreateServiceAccount(account)
	if err != nil {
		t.Error(err)
	}
	err = dut.DeleteServiceAccount(account)
	if err != nil {
		t.Error(err)
	}
}
func TestCreateUpdateAndDeleteServiceAccount(t *testing.T) {

	account := rustfs.ServiceAccount{
		AccessKey: randomString(8),
		SecretKey: "someSuperS3cret",
		Name:      randomString(8),
	}
	dut := getClient(t)
	err := dut.CreateServiceAccount(account)
	if err != nil {
		t.Error(err)
	}
	account.SecretKey = "insecureOne"
	err = dut.UpdateServiceAccount(account)
	if err != nil {
		t.Error(err)
	}
	err = dut.DeleteServiceAccount(account)
	if err != nil {
		t.Error(err)
	}
}
func TestCreateReadAndDeleteServiceAccount(t *testing.T) {

	account := rustfs.ServiceAccount{
		AccessKey: randomString(8),
		SecretKey: "someSuperS3cret",
		Name:      randomString(8),
	}
	dut := getClient(t)
	err := dut.CreateServiceAccount(account)
	if err != nil {
		t.Error(err)
	}
	reply, err := dut.ReadServiceAccount(account.AccessKey)
	if err != nil {
		t.Error(err)
	}
	if reply.Name != account.Name {
		t.Error("Read value not matching")
	}
	err = dut.DeleteServiceAccount(account)
	if err != nil {
		t.Error(err)
	}
}
