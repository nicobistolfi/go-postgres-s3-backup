#!/bin/bash

# Build PostgreSQL client tools for AWS Lambda ARM64
# Uses Amazon Linux 2 ARM64 to build compatible binaries

set -e

echo "Building PostgreSQL client tools for AWS Lambda ARM64..."

cd "$(dirname "$0")"

# Clean up any previous builds
rm -rf opt

# Create directories
mkdir -p opt/bin
mkdir -p opt/lib

# Use Docker to build PostgreSQL client tools on Amazon Linux 2 ARM64
if command -v docker >/dev/null 2>&1; then
    echo "Using Docker to build PostgreSQL client tools on Amazon Linux 2 ARM64..."
    
    docker run --rm --platform linux/arm64 -v "$(pwd):/workspace" arm64v8/amazonlinux:2 bash -c "
        set -e
        
        # Update package list
        yum update -y
        
        # Install required tools and libraries
        yum install -y wget which file zstd libzstd openssl-libs openssl11-libs
        
        # Download PostgreSQL 17 ARM64 binaries from the official PostgreSQL repository
        cd /tmp
        wget https://download.postgresql.org/pub/repos/yum/17/redhat/rhel-8-aarch64/postgresql17-17.5-3PGDG.rhel8.aarch64.rpm
        wget https://download.postgresql.org/pub/repos/yum/17/redhat/rhel-8-aarch64/postgresql17-libs-17.5-3PGDG.rhel8.aarch64.rpm
        
        # Extract RPMs without installing
        rpm2cpio postgresql17-libs-17.5-3PGDG.rhel8.aarch64.rpm | cpio -idmv >/dev/null 2>&1
        rpm2cpio postgresql17-17.5-3PGDG.rhel8.aarch64.rpm | cpio -idmv >/dev/null 2>&1
        
        # Verify extraction worked
        if [ ! -f /tmp/usr/pgsql-17/bin/pg_dump ]; then
            echo 'ERROR: pg_dump binary not found after extraction'
            ls -la /tmp/usr/ || echo 'No /tmp/usr directory'
            exit 1
        fi
        
        # Copy binaries
        cp /tmp/usr/pgsql-17/bin/pg_dump /workspace/opt/bin/
        cp /tmp/usr/pgsql-17/bin/pg_restore /workspace/opt/bin/
        cp /tmp/usr/pgsql-17/bin/psql /workspace/opt/bin/
        
        # Copy PostgreSQL libraries
        cp -r /tmp/usr/pgsql-17/lib/* /workspace/opt/lib/ 2>/dev/null || true
        
        # Copy OpenSSL 1.1 libraries explicitly
        cp /usr/lib64/libssl.so.1.1* /workspace/opt/lib/ 2>/dev/null || true
        cp /usr/lib64/libcrypto.so.1.1* /workspace/opt/lib/ 2>/dev/null || true
        
        # Use ldd to find and copy all required system libraries
        echo 'Finding and copying required libraries...'
        
        # Function to copy library dependencies
        copy_deps() {
            local binary=\$1
            local deps=\$(ldd \$binary 2>/dev/null | grep -o '/[^ ]*' | grep -v '^/workspace')
            
            for dep in \$deps; do
                if [ -f \"\$dep\" ] && [ ! -f \"/workspace/opt/lib/\$(basename \$dep)\" ]; then
                    cp \"\$dep\" /workspace/opt/lib/ 2>/dev/null || true
                fi
            done
        }
        
        # Copy dependencies for all binaries
        for bin in /workspace/opt/bin/*; do
            if [ -f \"\$bin\" ]; then
                echo \"Copying dependencies for \$(basename \$bin)...\"
                copy_deps \"\$bin\"
            fi
        done
        
        # Make binaries executable
        chmod +x /workspace/opt/bin/*
        
        echo 'Build completed successfully!'
        echo 'Binaries:'
        ls -la /workspace/opt/bin/
        echo 'Libraries:'
        ls -la /workspace/opt/lib/ | head -10
    "
    
    if [ -f "opt/bin/pg_dump" ]; then
        echo "✅ PostgreSQL client tools built successfully for ARM64"
        echo "Architecture check:"
        file opt/bin/pg_dump
        echo ""
        echo "Layer contents:"
        find opt -type f | head -20
    else
        echo "❌ Failed to build PostgreSQL client tools"
        exit 1
    fi
else
    echo "Docker not available. Please install Docker to build the PostgreSQL layer."
    exit 1
fi

echo "PostgreSQL layer is ready!"