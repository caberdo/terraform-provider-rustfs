# Make a bucket requester-pays: every request must then be signed by a caller
# that accepts the charge. Destroying this resource restores BucketOwner.
resource "rustfs_bucket" "requester_pays" {
  name = "my-requester-pays-bucket"
}

resource "rustfs_bucket_request_payment" "requester_pays" {
  bucket = rustfs_bucket.requester_pays.name
  payer  = "Requester"
}
