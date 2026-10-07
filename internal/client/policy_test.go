package client_test

import (
	"testing"

	"github.com/weinmann-emt/terraform-provider-rustfs/internal/client"
)

func TestCreateAndDeletePolicy(t *testing.T) {
	dut := getClient()
	actions := [1]string{
		"s3:GetObject",
	}
	resources :=
		[1]string{
			"arn:aws:s3:::bucket/*",
		}
	name := "test"
	statements := []client.PolicyStatement{
		{
			Effect:   "Allow",
			Action:   actions[:],
			Resource: resources[:],
		},
	}
	policy := client.Policy{
		Name:      name,
		Statement: statements,
	}
	err := dut.CreatePolicy(policy)
	if err != nil {
		t.Error(err)
	}

	read, _ := dut.ReadPolicy(policy.Name)
	if read.Name != policy.Name {
		t.Error("read back not working")
	}

	err = dut.DeletePolicy(name)
	if err != nil {
		t.Error(err)
	}
}
