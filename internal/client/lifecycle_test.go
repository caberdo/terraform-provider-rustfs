package client_test

import (
	"strings"
	"testing"
	"time"

	"github.com/weinmann-emt/terraform-provider-rustfs/internal/client"
)

func TestCreateUpdateDelete(t *testing.T) {
	name := randomString(8)
	name = strings.ToLower(name)
	days := 20
	dut := getClient()
	dut.CreateBucket(name)

	lifecycleConfig := client.LifecycleConfiguration{
		Rules: []client.LifecycleRule{
			{
				ID:     "TestRule",
				Status: "Enabled",
				Filter: client.LifecycleFilter{
					Prefix: "test",
				},
				Expiration: &client.LifecycleExpiration{
					Days: &days,
				},
			},
		},
	}

	err := dut.SetBucketLifecycleConfiguration(name, &lifecycleConfig)
	if err != nil {
		t.Error("Eror during create", err)
	}
	time.Sleep(5 * time.Second)

	lifecycleConfig.Rules[0].Filter.Prefix = ""
	lifecycleConfig.Rules = append(lifecycleConfig.Rules, client.LifecycleRule{
		ID:     "TestRule2",
		Status: "Disabled",
		Filter: client.LifecycleFilter{
			Prefix: "test",
		},
		Expiration: &client.LifecycleExpiration{
			Days: &days,
		},
	})
	err = dut.SetBucketLifecycleConfiguration(name, &lifecycleConfig)
	if err != nil {
		t.Error("Eror during update", err)
	}
	time.Sleep(5 * time.Second)

	err = dut.DeleteBucketLifecycleConfiguration(name)
	if err != nil {
		t.Error("Eror during delete", err)
	}

	dut.DeleteBucket(name)
}
