// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs_test

import (
	"context"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
)

func TestCreateUserAccount(t *testing.T) {

	account := rustfs.UserAccount{
		AccessKey: randomString(8),
		SecretKey: randomString(8),
	}
	dut := getClient(t)
	err := dut.CreateUserAccount(context.Background(), account)
	if err != nil {
		t.Error(err)
	}
	read, err := dut.ReadUserAccount(context.Background(), account.AccessKey)
	if err != nil {
		t.Error(err)
	}
	if read.Status != "enabled" {
		t.Error("wtf")
	}

	err = dut.DeleteUserAccount(context.Background(), account)
	if err != nil {
		t.Error(err)
	}
}

func TestCreateUserAccountWithGrp(t *testing.T) {

	account := rustfs.UserAccount{
		AccessKey: randomString(8),
		SecretKey: randomString(8),
		Policy:    "readwrite",
	}
	dut := getClient(t)
	err := dut.CreateUserAccount(context.Background(), account)
	if err != nil {
		t.Error(err)
	}
	err = dut.DeleteUserAccount(context.Background(), account)
	if err != nil {
		t.Error(err)
	}
}

func TestAddUserWithAccesKey(t *testing.T) {
	account := rustfs.UserAccount{
		AccessKey: randomString(8),
		SecretKey: randomString(8),
	}
	dut := getClient(t)
	err := dut.CreateUserAccount(context.Background(), account)
	if err != nil {
		t.Error(err)
	}

	service := rustfs.ServiceAccount{
		AccessKey:  randomString(8),
		SecretKey:  "someSuperS3cret",
		Name:       randomServiceAccountName(),
		TargetUser: account.AccessKey,
	}
	err = dut.CreateServiceAccount(context.Background(), service)
	if err != nil {
		t.Error(err)
	}

	err = dut.DeleteServiceAccount(context.Background(), service)
	if err != nil {
		t.Error(err)
	}
	err = dut.DeleteUserAccount(context.Background(), account)
	if err != nil {
		t.Error(err)
	}

}
