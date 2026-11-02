Title: Deploy: AWS via CloudFormation, free tier only
Labels: deploy

## Goal

A CloudFormation template provisions the app on AWS using only free-tier resources, so the deployment can be created and removed with one command.

## Acceptance criteria

- [ ] `cloudformation/` template creates the required resources (for example a single t2.micro/t3.micro-class instance or App Runner equivalent, security group, and a Postgres option within free tier)
- [ ] Every resource type used is documented as free-tier eligible, with the limit noted
- [ ] No secret values are in the template; secrets come from SSM Parameter Store or CloudFormation parameters with `NoEcho`
- [ ] A documented deploy command and a delete command exist
- [ ] A cost-safety note explains how to avoid charges after the free tier ends

## Out of scope

- Multi-region, load balancers, or RDS beyond the free tier

## Constraints

- Free tier only; no paid resources may be created by default
- Depends on the Containerise issue
