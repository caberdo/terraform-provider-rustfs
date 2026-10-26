# Serve a static site from a bucket: create the bucket, then attach the website
# configuration. Destroying rustfs_bucket_website removes the configuration but
# keeps the bucket and its objects.
resource "rustfs_bucket" "site" {
  name = "my-static-site"
}

resource "rustfs_bucket_website" "site" {
  bucket                = rustfs_bucket.site.name
  index_document_suffix = "index.html"
  error_document_key    = "error.html"

  routing_rule {
    condition {
      key_prefix_equals = "docs/"
    }

    redirect {
      replace_key_prefix_with = "documents/"
    }
  }
}

# Alternatively redirect every request to another host. redirect_all_requests_to
# cannot be combined with index/error documents or routing rules.
resource "rustfs_bucket" "redirect" {
  name = "my-redirect-bucket"
}

resource "rustfs_bucket_website" "redirect" {
  bucket = rustfs_bucket.redirect.name

  redirect_all_requests_to = {
    host_name = "example.com"
    protocol  = "https"
  }
}
