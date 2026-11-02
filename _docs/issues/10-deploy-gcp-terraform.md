Title: Deploy: Google Cloud via Terraform (alternative)
Labels: deploy

## Goal

An alternative deployment to Google Cloud described in Terraform: the container runs on Cloud Run and Postgres is provided by Cloud SQL or an equivalent managed service.

## Acceptance criteria

- [ ] `terraform/` configuration creates a Cloud Run service from the app image
- [ ] Database is provisioned and `DATABASE_URL` is injected without being committed
- [ ] Terraform state is not committed; a remote backend or local ignored state is documented
- [ ] `terraform plan` runs without errors given documented variables
- [ ] Cost notes are included and the README states this is an alternative to the AWS path

## Out of scope

- Production-grade networking and IAM hardening beyond the minimum

## Constraints

- Depends on the Containerise issue
- Variables for project ID and region are supplied by the user; no real project IDs are committed
