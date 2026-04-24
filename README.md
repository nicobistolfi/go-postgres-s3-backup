# Go PostgreSQL S3 Backup

![Backup Banner](https://nico.bistol.fi/static/images/shutdown.jpeg)

A serverless backup solution for PostgreSQL databases using AWS Lambda, with automatic daily, monthly, and yearly backup rotation to S3.

## Features

- ✅ Automated daily backups of your PostgreSQL database
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

2. **Configure database**
```bash
echo "DATABASE_URL=postgresql://user:pass@host:5432/dbname" > .env
```

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

| Variable | Description | Required | Default |
|----------|-------------|----------|----------|
| `DATABASE_URL` | PostgreSQL connection string | Yes | - |
| `BACKUP_BUCKET` | S3 bucket name (auto-configured by CloudFormation) | Auto | - |
| `DAILY_BACKUP_RETENTION_DAYS` | Number of days to retain daily backups | No | 7 |

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