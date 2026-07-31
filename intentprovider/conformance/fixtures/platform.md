# Platform CI role

We need an IAM role called `ci-runner` for our CI pipeline, plus an
inline policy on that role granting it permission to send messages to
our `ci-notifications` SQS queue (`@platform.aws_sqs_queue.ci-notifications`).

The policy's own `Resource` field must reference the queue by its real
ARN.

Keep everything in the `platform` stack.
