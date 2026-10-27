// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs_test

import (
	"context"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
)

func TestCreateAndDeletePolicy(t *testing.T) {
	dut := getClient(t)
	actions := [1]string{
		"s3:GetObject",
	}
	resources :=
		[1]string{
			"arn:aws:s3:::bucket/*",
		}
	name := "test"
	statements := []rustfs.PolicyStatement{
		{
			Effect:   "Allow",
			Action:   actions[:],
			Resource: resources[:],
		},
	}
	policy := rustfs.Policy{
		Name:      name,
		Statement: statements,
	}
	err := dut.CreatePolicy(context.Background(), policy)
	if err != nil {
		t.Error(err)
	}

	read, _ := dut.ReadPolicy(context.Background(), policy.Name)
	if read.Name != policy.Name {
		t.Error("read back not working")
	}

	err = dut.DeletePolicy(context.Background(), name)
	if err != nil {
		t.Error(err)
	}
}
