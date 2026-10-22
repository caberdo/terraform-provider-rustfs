`rustfs_bucket_lifecycle_configuration` now accepts a `rule` list whose value is
unknown at plan time, so the resource works when the rules come from `count`,
`for_each` or any computed value. A known list that still holds an unknown rule
element is accepted too. The `rule` attribute was backed by a native Go slice
that could not hold an unknown value, so such a configuration failed to decode
with "Received unknown value ... Target Type: []provider.ruleModel"; it is now a
framework list decoded per boundary. An unknown rule value that unexpectedly
survives to apply is refused instead of being written to the bucket as an empty
rule list.
