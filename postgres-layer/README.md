# PostgreSQL Layer for AWS Lambda (ARM64)

This layer provides PostgreSQL client tools (pg_dump, pg_restore, psql) for AWS Lambda functions running on ARM64 architecture.

## Building the Layer

Run the build script to create the layer:

```bash
./build.sh
```

This script will:
1. Use Docker to run Ubuntu 20.04 ARM64
2. Install PostgreSQL client tools
3. Copy the binaries and all required libraries
4. Package them in the correct structure for Lambda layers

## Structure

After building, the layer will have this structure:
```
postgres-layer/
└── opt/
    ├── bin/
    │   ├── pg_dump
    │   ├── pg_restore
    │   └── psql
    └── lib/
        └── (various .so library files)
```

## Usage

The layer is automatically deployed with the Lambda function via Serverless Framework. The binaries will be available at:
- `/opt/opt/bin/pg_dump`
- `/opt/opt/bin/pg_restore`
- `/opt/opt/bin/psql`

## Architecture

This layer is built specifically for ARM64 architecture to match the Lambda function architecture.