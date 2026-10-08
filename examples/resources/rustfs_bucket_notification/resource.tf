# Notify an SQS-style queue and an SNS-style topic with key-name filtering, and
# forward events to Amazon EventBridge.
resource "rustfs_bucket" "events" {
  name = "my-event-bucket"
}

resource "rustfs_bucket_notification" "example" {
  bucket = rustfs_bucket.events.name

  queue = [
    {
      id     = "primary-queue"
      arn    = "arn:minio:sqs::PRIMARY:amqp"
      events = ["s3:ObjectCreated:*", "s3:ObjectRemoved:*"]

      filter = {
        prefix = "uploads/"
        suffix = ".jpg"
      }
    },
  ]

  topic = [
    {
      id     = "primary-topic"
      arn    = "arn:minio:sns::PRIMARY:topic"
      events = ["s3:ObjectCreated:Put"]
    },
  ]

  event_bridge = {
    event_bridge_enabled = true
  }
}

# Invoke a Lambda-style function on object creation, skipping server-side
# validation of the destination ARN.
resource "rustfs_bucket" "lambda_events" {
  name = "my-lambda-event-bucket"
}

resource "rustfs_bucket_notification" "lambda" {
  bucket                      = rustfs_bucket.lambda_events.name
  skip_destination_validation = true

  lambda = [
    {
      id     = "process-upload"
      arn    = "arn:minio:lambda::PRIMARY:process-upload"
      events = ["s3:ObjectCreated:Put"]

      filter = {
        prefix = "incoming/"
      }
    },
  ]
}