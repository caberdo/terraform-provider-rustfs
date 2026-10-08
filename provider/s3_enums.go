package provider

// enumStrings converts an SDK enum slice (as returned by the generated
// Values() methods on aws-sdk-go-v2 enum types) into the []string that
// terraform-plugin-framework validators such as stringvalidator.OneOf expect.
//
// Usage:
//
//	stringvalidator.OneOf(enumStrings(s3types.BucketCannedACL("").Values())...)
func enumStrings[T ~string](vals []T) []string {
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = string(v)
	}
	return out
}
