#!/bin/bash
# Workspace status script for Bazel stamping
# This script outputs key-value pairs that can be used for build stamping

# Get version from environment variable (set during release builds)
# or fall back to git describe or "dev"
if [ -n "${GALA_VERSION}" ]; then
    echo "STABLE_GALA_VERSION ${GALA_VERSION}"
else
    VERSION=$(git describe --tags --always 2>/dev/null || echo "dev")
    echo "STABLE_GALA_VERSION ${VERSION}"
fi

# Git commit SHA
COMMIT=$(git rev-parse HEAD 2>/dev/null || echo "unknown")
echo "STABLE_GIT_COMMIT ${COMMIT}"

# Build timestamp
echo "STABLE_BUILD_DATE $(date -u +%Y-%m-%dT%H:%M:%SZ)"
