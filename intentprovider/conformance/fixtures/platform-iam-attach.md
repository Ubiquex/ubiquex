# CI artifacts platform

Infrastructure for our CI pipeline in eu-central-1:

1. An ECR repository called "ci-artifacts" for container images.
   Enable image scanning on push. Images should be immutable.

2. An SQS queue called "ci-notifications" for pipeline events.
   Standard queue, messages kept for 1 day.

3. An IAM role called "ci-runner" that EC2 instances can assume.

4. A custom IAM policy called "ci-runner-access" that allows:
   - pushing and pulling images to/from the ci-artifacts repository (only that one)
   - sending messages to the ci-notifications queue (only that one)
   Attach this policy to the ci-runner role.
