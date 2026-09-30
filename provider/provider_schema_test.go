package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestProviderSchemaSecretKeyAlias(t *testing.T) {
	p := &RustfsProvider{}
	var resp provider.SchemaResponse
	p.Schema(context.Background(), provider.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}

	secretKey, ok := resp.Schema.Attributes["secret_key"]
	if !ok {
		t.Fatal("expected secret_key attribute in provider schema")
	}
	secretKeyAttr, ok := secretKey.(schema.StringAttribute)
	if !ok {
		t.Fatalf("expected StringAttribute for secret_key, got %T", secretKey)
	}
	if !secretKeyAttr.Sensitive {
		t.Error("secret_key should be marked sensitive")
	}

	accessSecret, ok := resp.Schema.Attributes["access_secret"]
	if !ok {
		t.Fatal("expected access_secret attribute in provider schema")
	}
	accessSecretAttr, ok := accessSecret.(schema.StringAttribute)
	if !ok {
		t.Fatalf("expected StringAttribute for access_secret, got %T", accessSecret)
	}
	if accessSecretAttr.DeprecationMessage == "" {
		t.Error("access_secret should have a deprecation message")
	}
}

func TestProviderModelSecretKeyPrecedence(t *testing.T) {
	model := RustfsProviderModel{
		SecretKey:    types.StringValue("new-secret"),
		AccessSecret: types.StringValue("legacy-secret"),
	}
	if got := model.secretKey(); got != "new-secret" {
		t.Errorf("expected secret_key to take precedence, got %q", got)
	}

	model.SecretKey = types.StringNull()
	if got := model.secretKey(); got != "legacy-secret" {
		t.Errorf("expected access_secret fallback, got %q", got)
	}

	model.AccessSecret = types.StringNull()
	if got := model.secretKey(); got != "" {
		t.Errorf("expected empty string when neither set, got %q", got)
	}
}
