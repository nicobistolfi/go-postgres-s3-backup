# CloudFormation Migration Design

**Status:** approved
**Date:** 2026-04-24
**Author:** Nico Bistolfi (with Claude)

## Goal

Replace the Serverless Framework deployment with a native AWS CloudFormation stack, using the `go-textract-api` project's CloudFormation setup as the reference pattern. The outcome is a single deployable template plus `task cf:*` commands that provision identical infrastructure (scheduled Lambda + backup S3 bucket + Postgres tools layer).

## Non-goals

- Adding features beyond what the current `serverless.yml` deploys.
- Refactoring the Go Lambda handler (`cmd/lambda/main.go`).
- Migrating an existing live deployment. Users with an existing Serverless-deployed stack are expected to redeploy fresh; the existing backup bucket is theirs to delete or import manually.

## Architecture

Single CloudFormation template at `cloudformation/template.yml`. Deployed via the pattern from `go-textract-api`:

1. `task build:lambda` → produces the `bootstrap` arm64 binary (depends on `build:postgres-layer`, which runs `postgres-layer/build.sh`).
2. `task cf:package` → zips `bootstrap` → `bootstrap.zip` and the built layer tree → `postgres-layer.zip`, ensures an artifact S3 bucket exists (auto-created if missing), then runs `aws cloudformation package` to upload both zips and write `cloudformation/template.packaged.yml` with resolved S3 URIs.
3. `task cf:deploy` → runs `aws cloudformation deploy` against the packaged template with `--parameter-overrides` for `Stage`, `DatabaseUrl` (NoEcho), `DailyBackupRetentionDays`, etc., and prints stack outputs.

Artifact bucket naming: `go-postgres-s3-backup-artifacts-<account-id>-<region>`.

Stack name: `go-postgres-s3-backup-<stage>`.

## CloudFormation template

### Parameters

| Parameter | Type | Default | Notes |
|---|---|---|---|
| `Stage` | String | `dev` | Resource name suffix |
| `DatabaseUrl` | String | — | `NoEcho: true`; passed to Lambda as `DATABASE_URL` |
| `DailyBackupRetentionDays` | Number | `7` | Passed to Lambda as `DAILY_BACKUP_RETENTION_DAYS` |
| `MemorySize` | Number | `512` | |
| `Timeout` | Number | `300` | 5-minute Lambda timeout |
| `LogRetentionDays` | Number | `14` | CloudWatch log group retention |

### Resources

- **`BackupBucket`** (`AWS::S3::Bucket`)
  - `DeletionPolicy: Retain`, `UpdateReplacePolicy: Retain` — backups survive stack deletion.
  - `BucketName: !Sub 'go-postgres-s3-backup-${Stage}-backups'`.
  - `BucketEncryption`: AES256 SSE.
  - `VersioningConfiguration: { Status: Enabled }`.
  - `PublicAccessBlockConfiguration`: all four flags true.
  - `LifecycleConfiguration.Rules`:
    - `TransitionMonthlyToGlacier`: prefix `monthly/`, transition to `GLACIER` after 30 days.
    - `TransitionYearlyToDeepArchive`: prefix `yearly/`, transition to `DEEP_ARCHIVE` after 90 days.

- **`PostgresLayer`** (`AWS::Lambda::LayerVersion`)
  - `LayerName: !Sub 'go-postgres-s3-backup-postgres-${Stage}'`.
  - `Content: ../postgres-layer.zip` (resolved to an S3 location by `aws cloudformation package`).
  - `CompatibleRuntimes: [provided.al2]`.
  - `CompatibleArchitectures: [arm64]`.
  - `Description: 'PostgreSQL client tools (pg_dump, psql)'`.

- **`LambdaExecutionRole`** (`AWS::IAM::Role`)
  - `RoleName: !Sub 'go-postgres-s3-backup-${Stage}-role'`.
  - Trust policy for `lambda.amazonaws.com`.
  - Inline `LambdaExecutionPolicy`:
    - `logs:CreateLogGroup`, `logs:CreateLogStream`, `logs:PutLogEvents` on `arn:aws:logs:*:*:*`.
    - `s3:PutObject`, `s3:GetObject`, `s3:DeleteObject`, `s3:ListBucket`, `s3:HeadObject` on `!GetAtt BackupBucket.Arn` and `!Sub '${BackupBucket.Arn}/*'`.

- **`BackupLogGroup`** (`AWS::Logs::LogGroup`)
  - `LogGroupName: !Sub '/aws/lambda/go-postgres-s3-backup-${Stage}'`.
  - `RetentionInDays: !Ref LogRetentionDays`.

- **`BackupFunction`** (`AWS::Lambda::Function`)
  - `DependsOn: BackupLogGroup`.
  - `FunctionName: !Sub 'go-postgres-s3-backup-${Stage}'`.
  - `Runtime: provided.al2`, `Architectures: [arm64]`, `Handler: bootstrap`.
  - `Code: ../bootstrap.zip`.
  - `Role: !GetAtt LambdaExecutionRole.Arn`.
  - `MemorySize: !Ref MemorySize`, `Timeout: !Ref Timeout`.
  - `Layers: [!Ref PostgresLayer]`.
  - `Environment.Variables`: `DATABASE_URL`, `BACKUP_BUCKET: !Ref BackupBucket`, `DAILY_BACKUP_RETENTION_DAYS`.

- **`ScheduleRule`** (`AWS::Events::Rule`)
  - `ScheduleExpression: cron(0 2 * * ? *)`.
  - `Description: Daily PostgreSQL database backup`.
  - `State: ENABLED`.
  - `Targets: [{ Arn: !GetAtt BackupFunction.Arn, Id: BackupFunctionTarget }]`.

- **`ScheduleInvokePermission`** (`AWS::Lambda::Permission`)
  - `Action: lambda:InvokeFunction`, `Principal: events.amazonaws.com`, `SourceArn: !GetAtt ScheduleRule.Arn`.

### Outputs

- `FunctionName` — `!Ref BackupFunction`.
- `FunctionArn` — `!GetAtt BackupFunction.Arn`.
- `BackupBucketName` — `!Ref BackupBucket`.

## Taskfile

### New variables

```yaml
STACK_PREFIX: "go-postgres-s3-backup"
CF_TEMPLATE: "cloudformation/template.yml"
CF_PACKAGED_TEMPLATE: "cloudformation/template.packaged.yml"
LAMBDA_ZIP: "bootstrap.zip"
LAYER_ZIP: "postgres-layer.zip"
```

### New tasks

- **`cf:package`** — `deps: [build:lambda]` (which depends on `build:postgres-layer`, producing `postgres-layer/opt/{bin,lib}/`). Zips `bootstrap` → `bootstrap.zip` with `zip -j bootstrap.zip bootstrap`. Zips the layer preserving the `opt/` prefix at the zip root: `cd postgres-layer && zip -r ../postgres-layer.zip opt`. This matches the Serverless Framework layout the handler expects — Lambda extracts layer zips into `/opt/`, so `opt/bin/pg_dump` in the zip lands at `/opt/opt/bin/pg_dump`, which is exactly what `cmd/lambda/main.go` looks for via `PATH=/opt/opt/bin`. Ensures artifact bucket exists (creates if missing); runs `aws cloudformation package --template-file $CF_TEMPLATE --s3-bucket <artifact-bucket> --output-template-file $CF_PACKAGED_TEMPLATE --region $REGION`.
- **`cf:deploy`** — `deps: [cf:package]`. Validates `DATABASE_URL` is set (fails loudly if not). Runs `aws cloudformation deploy --template-file $CF_PACKAGED_TEMPLATE --stack-name $STACK_PREFIX-$STAGE --parameter-overrides Stage=$STAGE DatabaseUrl="$DATABASE_URL" DailyBackupRetentionDays="${DAILY_BACKUP_RETENTION_DAYS:-7}" --capabilities CAPABILITY_NAMED_IAM --region $REGION --no-fail-on-empty-changeset`. Then prints stack outputs via `aws cloudformation describe-stacks`.
- **`cf:remove`** — `aws cloudformation delete-stack` + `aws cloudformation wait stack-delete-complete`. Echoes a reminder that the backup bucket was retained and must be deleted manually if no longer needed.
- **`cf:logs`** — `aws logs tail /aws/lambda/$STACK_PREFIX-$STAGE --follow --region $REGION`.
- **`cf:invoke`** — `aws lambda invoke --function-name $STACK_PREFIX-$STAGE --region $REGION /tmp/invoke-out.json` then `cat /tmp/invoke-out.json`.

### Aliases

- `deploy` → `cf:deploy`
- `logs` → `cf:logs`
- `invoke` → `cf:invoke`

### Updated `clean`

Adds `rm -f bootstrap.zip postgres-layer.zip cloudformation/template.packaged.yml` alongside existing removals.

### Removed

- `serverless:deploy`, `serverless:remove`, `serverless:logs`, `serverless:invoke` tasks and their `sls:*` aliases.
- The current `deploy` / `logs` / `invoke` aliases that point at `serverless:*` (they're redefined above pointing at `cf:*`).

## Files changed

**Added:**
- `cloudformation/template.yml`

**Modified:**
- `Taskfile.yml` — task swap described above.
- `README.md` — replace Serverless Quick Start / Manual Operations commands with `task cf:deploy` / `task cf:logs` / `task cf:invoke`; drop Node.js + `npm install -g serverless` from Prerequisites; add AWS CLI as required (it already is, but called out explicitly).
- `.gitignore` — add `bootstrap.zip`, `postgres-layer.zip`, `cloudformation/template.packaged.yml` if not already ignored.

**Removed:**
- `serverless.yml`
- `package.json` (exists only to pin Serverless Framework — confirmed)

## Migration notes

This is a clean replacement. A user with an existing Serverless-deployed stack has two options (both out-of-scope for the implementation, but called out in the README):

1. **Fresh deploy, lose history** — `serverless remove` the old stack, then `task cf:deploy`. A new backup bucket is created with a different name (both use the same naming pattern, but the old stack's bucket is deleted with it unless `DeletionPolicy` was set — it wasn't in the Serverless version).
2. **Preserve bucket** — before running `serverless remove`, change the Serverless bucket's retention / remove the bucket from the Serverless stack, then use `aws cloudformation create-change-set --change-set-type IMPORT` to import the existing bucket into the new CF stack under the `BackupBucket` logical id.

Assume (1) unless the user asks for import tooling.

## Testing / verification

- `task build:lambda` builds cleanly (no Serverless dependencies pulled in).
- `task cf:package` produces `bootstrap.zip`, `postgres-layer.zip`, and `cloudformation/template.packaged.yml` with S3 URIs substituted.
- `aws cloudformation validate-template --template-body file://cloudformation/template.yml` passes.
- `task cf:deploy STAGE=dev` deploys successfully; stack outputs show the function name, ARN, and bucket name.
- `task cf:invoke STAGE=dev` triggers a backup; `task cf:logs STAGE=dev` shows successful completion.
- Manual check: a new object appears under `s3://<BackupBucketName>/daily/`.
- `task cf:remove STAGE=dev` deletes the stack; the backup bucket remains.
