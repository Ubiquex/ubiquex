# Platform CI, via a named blueprint call

Call blueprint `ci-platform` as `platform` with: repo_name =
payments-ci-artifacts, queue_name = payments-notifications.

We also need an IAM role policy granting a downstream service access to
that repository -- attach a policy to the `downstream-role` role using
platform's own `repo_arn` output as the `Resource` in an inline policy
granting `s3:GetObject`.

Keep everything in the `platform-blueprint-output` stack.
