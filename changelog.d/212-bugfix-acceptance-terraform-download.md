The acceptance fixture installs Terraform with `wget -t 3` instead of the curl-only
`--retry`/`--retry-delay` options. A fresh `make testacc-docker` or `make testacc-matrix`
run on a host without a cached Terraform binary now downloads it instead of failing with
`wget: unrecognized option '--retry-delay'`.
