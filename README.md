# Go PostgreSQL S3 Backup

![Backup Banner](shutdown.jpeg)

A serverless backup solution for PostgreSQL databases using AWS Lambda, with automatic daily, monthly, and yearly backup rotation to S3.

## Features

- ✅ Automated daily backups of your PostgreSQL database
- ✅ On-demand backups via an authenticated HTTP endpoint (`GET /run`)
- ✅ Intelligent backup rotation (daily, monthly, yearly)
- ✅ Daily backups retained for configurable period (default 7 days)
- ✅ Monthly backups automatically transitioned to Glacier storage
- ✅ Yearly backups moved to Deep Archive for long-term retention
- ✅ Deployed via AWS CloudFormation
- ✅ S3 bucket encryption and versioning enabled
- ✅ Uses pgx/v5 for efficient PostgreSQL connectivity

## Project Structure

```
/
├── cmd/
│   └── lambda/
│       └── main.go           # Lambda function entry point
├── cloudformation/
│   └── template.yml          # CloudFormation stack definition
├── postgres-layer/           # Lambda layer with pg_dump/psql
├── Taskfile.yml              # Task runner configuration
├── .env                      # Environment variables (not in repo)
├── .gitignore                # Git ignore file
├── go.mod                    # Go module file
└── README.md                 # This file
```

## Prerequisites

- Go 1.21+
- [Task](https://taskfile.dev)
- Docker (for building the PostgreSQL layer)
- AWS CLI configured

## Quick Start

1. **Clone and setup**
```bash
git clone https://github.com/nicobistolfi/go-postgres-s3-backup.git
cd go-postgres-s3-backup
```

2. **Configure environment**
```bash
cat > .env <<'ENV'
DATABASE_URL=postgresql://user:pass@host:5432/dbname
API_KEY=change-me-to-a-long-random-secret
ENV
```
`DATABASE_URL` and `API_KEY` are both required to deploy. See [Environment Variables](#environment-variables) for the full list.

3. **Deploy**
```bash
task deploy
```

That's it! Your PostgreSQL database will be backed up daily at 2 AM UTC.

## How It Works

1. **Daily Execution**: The Lambda function runs daily at 2 AM UTC via EventBridge
2. **Database Backup**: Connects to your PostgreSQL database and creates a SQL dump
3. **Daily Backup**: Saves the backup to S3 under `daily/YYYY-MM-DD-backup.sql`
4. **Monthly Backup**: If no backup exists for the current month, copies the daily backup to `monthly/YYYY-MM-backup.sql`
5. **Yearly Backup**: If no backup exists for the current year, copies the daily backup to `yearly/YYYY-backup.sql`
6. **Cleanup**: Removes daily backups older than configured retention period (default 7 days)
7. **Lifecycle Management**: 
   - Monthly backups transition to Glacier after 30 days
   - Yearly backups transition to Deep Archive after 90 days

Besides the daily schedule, you can trigger a backup on demand through the authenticated [`/run` HTTP endpoint](#trigger-a-backup-over-http). A manual run always stores today's daily backup (even if the dump matches an older backup), unless today's backup already holds identical content.

## Screenshots

### S3 Bucket Structure
![S3 Backups Overview](docs/go-postgres-s3-backup-backups.png)

### Daily Backups
![Daily Backups](docs/go-postgres-s3-backup-backups-daily.png)

## Manual Operations

**Trigger a backup:**
```bash
task invoke
```

**View logs:**
```bash
task logs
```

**Remove deployment:**
```bash
task cf:remove
```

> The S3 backup bucket is retained on stack removal. Delete it manually if you no longer need the backups.

### Trigger a backup over HTTP

Deploying creates an HTTP API (API Gateway v2) with a single authenticated route:

```
GET /run
```

The endpoint URL is printed as the `RunEndpoint` stack output after `task deploy`. Authenticate with your `API_KEY`, supplied either as a header or a query parameter:

```bash
# As a header
curl -H "X-Api-Key: $API_KEY" "$RUN_ENDPOINT"

# As a query parameter
curl "$RUN_ENDPOINT?api_key=$API_KEY"
```

Unlike the scheduled run, a manual `/run` always stores today's daily backup even when the dump matches an older backup — except when today's backup already holds identical content, which is skipped to avoid a redundant copy.

On success it returns `200` with a JSON summary of the run:

```json
{
  "status": "ok",
  "action": "created",
  "reason": "content changed",
  "key": "daily/2026-05-27-backup.sql",
  "size": "1.50 MB",
  "size_bytes": 1572864,
  "duration_ms": 4231
}
```

| Field | Meaning |
|-------|---------|
| `action` | `created` if a daily backup was written, `skipped` if nothing was stored |
| `reason` | Why the backup was created/skipped: `content changed`, `unchanged`, `today's backup already identical`, or `forced; matched an older backup` |
| `key` | S3 key of today's daily backup |
| `size` / `size_bytes` | Dump size — human-readable (KB/MB/GB) and exact byte count, so you can spot size changes between runs |
| `duration_ms` | Wall-clock time of the run |

A missing or invalid key returns `401`; a backup failure returns `500` with an `error` message.

## Monitoring

### View recent backups

```bash
aws s3 ls s3://go-postgres-s3-backup-[stage]-backups/daily/
aws s3 ls s3://go-postgres-s3-backup-[stage]-backups/monthly/
aws s3 ls s3://go-postgres-s3-backup-[stage]-backups/yearly/
```

### Download a backup

```bash
aws s3 cp s3://go-postgres-s3-backup-[stage]-backups/daily/2025-08-01-backup.sql ./
```

## Testing Backups Locally

**Start local PostgreSQL:**
```bash
docker run --name my-postgres -e POSTGRES_PASSWORD=postgres -d -p 5432:5432 postgres
```

**Restore backup to local instance:**
```bash
docker exec -i my-postgres psql -U postgres -d postgres -W < [backup-file].sql
```

**Connect and query:**
```bash
docker exec -it my-postgres psql -U postgres
```

## Environment Variables

Configuration lives in a `.env` file in the project root. `Task` loads it for the deploy/local commands, `task deploy` forwards the relevant values into the deployed Lambda as CloudFormation parameters, and the Lambda also reads `.env` directly when run locally (`task run`).

| Variable | Description & why | Required | Default |
|----------|------------------|----------|---------|
| `DATABASE_URL` | PostgreSQL connection string (`postgresql://user:pass@host:5432/dbname`). This is the database `pg_dump` connects to — the core input of every backup. | Yes | - |
| `API_KEY` | Secret that protects the `/run` HTTP endpoint. Callers must present it via the `X-Api-Key` header or `api_key` query parameter; the Lambda compares it in constant time. Use a long random string. | Yes | - |
| `DAILY_BACKUP_RETENTION_DAYS` | How many days of `daily/` backups to keep. Older daily objects are pruned after each successful run, keeping storage (and cost) bounded. | No | 7 |
| `STAGE` | Deployment stage used as a suffix for the stack and resource names (e.g. `dev`, `prod`). Lets you run isolated deployments side by side. | No | dev |
| `REGION` | AWS region to deploy into and operate against. | No | us-west-1 |
| `ARTIFACT_BUCKET` | S3 bucket that holds the packaged Lambda/layer zip during `task deploy`. Created automatically if it doesn't exist; override only if you want a specific bucket. | No | `go-postgres-s3-backup-artifacts-<account>-<region>` |
| `BACKUP_BUCKET` | S3 bucket that stores the backups. Auto-configured by CloudFormation inside the deployed Lambda — you only need to set this in `.env` for local runs (`task run`). | Auto | - |

### Example `.env`

```bash
# Required
DATABASE_URL=postgresql://user:pass@db-host:5432/mydb
API_KEY=change-me-to-a-long-random-secret

# Optional (defaults shown)
DAILY_BACKUP_RETENTION_DAYS=7
STAGE=dev
REGION=us-west-1
```

## Security

- Database credentials are stored as Lambda environment variables
- S3 bucket has encryption enabled (AES256)
- Public access to the S3 bucket is blocked
- IAM role follows least privilege principle
- Versioning is enabled on the S3 bucket

## Cost Optimization

- Lambda runs only once per day (minimal compute costs)
- Daily backups are automatically deleted after the retention period (configurable, default 7 days)
- Monthly backups move to cheaper Glacier storage
- Yearly backups move to Deep Archive for maximum cost savings

## Troubleshooting

### Lambda timeout issues

If your database is large and backups are timing out:
1. Increase the `Timeout` parameter when deploying (default 300 seconds) or the default in `cloudformation/template.yml`
2. Consider increasing the `MemorySize` parameter (default 512 MB)

### Connection issues

Ensure your PostgreSQL database allows connections from AWS Lambda:
1. Check your PostgreSQL connection pooling settings
2. Verify the DATABASE_URL is correct
3. Ensure your database is not hitting connection limits

### Missing backups

Check the Lambda logs for errors:
```bash
task logs
```


## License

This project is licensed under the MIT License - see the LICENSE file for details.