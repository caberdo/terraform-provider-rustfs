package client_test

import (
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/weinmann-emt/terraform-provider-rustfs/internal/client"
)

func getClient() client.RustfsAdmin {
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
	return dut
}

func randomString() string {
	const length = 8
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	const charset = letters + "0123456789"
	result := make([]byte, length)

	// Access keys and names must start with a letter.
	result[0] = letters[rng.Intn(len(letters))]
	for i := 1; i < length; i++ {
		result[i] = charset[rng.Intn(len(charset))]
	}

	return string(result)
}

func TestCreateServiceAccount(t *testing.T) {

	account := client.ServiceAccount{
		AccessKey: randomString(),
		SecretKey: "someSuperS3cret",
		Name:      randomString(),
	}
	dut := getClient()
	err := dut.CreateServiceAccount(account)
	if err != nil {
		t.Error(err)
	}
}

func TestCreateAndDeleteServiceAccount(t *testing.T) {

	account := client.ServiceAccount{
		AccessKey: randomString(),
		SecretKey: "someSuperS3cret",
		Name:      randomString(),
	}
	dut := getClient()
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

	account := client.ServiceAccount{
		AccessKey: randomString(),
		SecretKey: "someSuperS3cret",
		Name:      randomString(),
	}
	dut := getClient()
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

	account := client.ServiceAccount{
		AccessKey: randomString(),
		SecretKey: "someSuperS3cret",
		Name:      randomString(),
	}
	dut := getClient()
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
