// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// providerTypeName is the Terraform type name prefix every registered resource
// and data source derives its own type name from.
const providerTypeName = "rustfs"

// The registry keeps provider.go independent of the type list: a feature adds
// one file that constructs its resource or data source and registers it from
// init, so parallel feature branches never edit the same list.
var (
	registryMu          sync.RWMutex
	resourceFactories   []func() resource.Resource
	dataSourceFactories []func() datasource.DataSource
)

// registerResource adds a resource constructor to the provider registry. Call
// it from init in the file that owns the resource.
func registerResource(factory func() resource.Resource) {
	registryMu.Lock()
	defer registryMu.Unlock()

	resourceFactories = append(resourceFactories, factory)
}

// registerDataSource adds a data source constructor to the provider registry.
// Call it from init in the file that owns the data source.
func registerDataSource(factory func() datasource.DataSource) {
	registryMu.Lock()
	defer registryMu.Unlock()

	dataSourceFactories = append(dataSourceFactories, factory)
}

// registeredResources returns the registered resource constructors sorted by
// Terraform type name, so Resources() is deterministic.
//
// Metadata() only resolves a type name from a local schema and performs no I/O;
// Resources() and DataSources() receive no caller context, so the background
// context used for that call (here, below and in RegistrySnapshot) is the one
// documented exception to the ctx-first client rule.
func registeredResources() []func() resource.Resource {
	registryMu.RLock()
	factories := append([]func() resource.Resource(nil), resourceFactories...)
	registryMu.RUnlock()

	return sortedByTypeName(factories, func(r resource.Resource) string {
		var resp resource.MetadataResponse
		r.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: providerTypeName}, &resp)
		return resp.TypeName
	})
}

// registeredDataSources returns the registered data source constructors sorted
// by Terraform type name, so DataSources() is deterministic.
func registeredDataSources() []func() datasource.DataSource {
	registryMu.RLock()
	factories := append([]func() datasource.DataSource(nil), dataSourceFactories...)
	registryMu.RUnlock()

	return sortedByTypeName(factories, func(d datasource.DataSource) string {
		var resp datasource.MetadataResponse
		d.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: providerTypeName}, &resp)
		return resp.TypeName
	})
}

// sortedByTypeName resolves each factory's Terraform type name once and returns
// the factories ordered by it.
func sortedByTypeName[T any](factories []func() T, typeName func(T) string) []func() T {
	type namedFactory struct {
		typeName string
		factory  func() T
	}

	named := make([]namedFactory, len(factories))
	for i, factory := range factories {
		named[i] = namedFactory{typeName: typeName(factory()), factory: factory}
	}
	sort.SliceStable(named, func(i, j int) bool {
		return named[i].typeName < named[j].typeName
	})

	sorted := make([]func() T, len(named))
	for i, entry := range named {
		sorted[i] = entry.factory
	}
	return sorted
}

// RegistryKind distinguishes the two kinds of type the provider registers.
type RegistryKind string

const (
	RegistryKindResource   RegistryKind = "resource"
	RegistryKindDataSource RegistryKind = "data source"
)

// RegistryEntry describes one registered Terraform type. File is the
// repository-relative path of the Go file that defines the factory, or an
// empty string when the path cannot be resolved; Description is the type's
// schema description.
type RegistryEntry struct {
	TypeName    string
	Kind        RegistryKind
	File        string
	Description string
}

// RegistrySnapshot returns every registered resource and data source together
// with the file that defines it and its schema description, sorted by type
// name. The generated tables in AGENTS.md and README.md are built from it, so
// no hand-maintained type-to-file mapping exists anywhere.
func RegistrySnapshot() []RegistryEntry {
	registryMu.RLock()
	resources := append([]func() resource.Resource(nil), resourceFactories...)
	dataSources := append([]func() datasource.DataSource(nil), dataSourceFactories...)
	registryMu.RUnlock()

	ctx := context.Background()
	entries := make([]RegistryEntry, 0, len(resources)+len(dataSources))

	for _, factory := range resources {
		r := factory()
		var meta resource.MetadataResponse
		r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: providerTypeName}, &meta)

		var schema resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &schema)

		entries = append(entries, RegistryEntry{
			TypeName:    meta.TypeName,
			Kind:        RegistryKindResource,
			File:        factoryFile(factory),
			Description: schema.Schema.Description,
		})
	}

	for _, factory := range dataSources {
		d := factory()
		var meta datasource.MetadataResponse
		d.Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: providerTypeName}, &meta)

		var schema datasource.SchemaResponse
		d.Schema(ctx, datasource.SchemaRequest{}, &schema)

		entries = append(entries, RegistryEntry{
			TypeName:    meta.TypeName,
			Kind:        RegistryKindDataSource,
			File:        factoryFile(factory),
			Description: schema.Schema.Description,
		})
	}

	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].TypeName < entries[j].TypeName
	})
	return entries
}

// factoryFile reports the repository-relative Go file that defines factory.
func factoryFile(factory any) string {
	value := reflect.ValueOf(factory)
	if value.Kind() != reflect.Func || value.IsNil() {
		return ""
	}

	fn := runtime.FuncForPC(value.Pointer())
	if fn == nil {
		return ""
	}

	file, _ := fn.FileLine(fn.Entry())
	file = filepath.ToSlash(file)

	// Strip the build prefix, or a -trimpath module prefix, so the table only
	// ever shows provider/<file>.go.
	if index := strings.LastIndex(file, "/provider/"); index >= 0 {
		return file[index+1:]
	}
	if strings.HasPrefix(file, "provider/") {
		return file
	}
	return ""
}
