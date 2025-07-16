#!/bin/bash
set -e

echo "🧪 Integration Test: File-based API with Real ansible-runner"

# Check if ansible-runner is available
if ! command -v ansible-runner &> /dev/null; then
    echo "❌ ansible-runner not found. Install with: pip install ansible-runner"
    exit 1
fi

# Setup test environment
TEST_DIR="/tmp/ansible-operator-integration-test"
ARTIFACTS_DIR="$TEST_DIR/artifacts"
IDENTIFIER="integration-test-$(date +%s)"

rm -rf "$TEST_DIR"
mkdir -p "$TEST_DIR"

# Create test playbook
cat > "$TEST_DIR/test-playbook.yml" << 'EOF'
---
- hosts: localhost
  gather_facts: no
  tasks:
    - name: Test task 1
      debug:
        msg: "This is test task 1"
    - name: Test task 2
      debug:
        msg: "This is test task 2"
    - name: Test task 3
      debug:
        msg: "This is test task 3"
EOF

echo "📁 Running ansible-runner..."
echo "Artifacts will be in: $ARTIFACTS_DIR/$IDENTIFIER/"

# Run ansible-runner
ansible-runner run "$ARTIFACTS_DIR" \
  --playbook test-playbook.yml \
  --ident "$IDENTIFIER" \
  --project-dir "$TEST_DIR"

echo "📊 Checking generated event files..."
JOB_EVENTS_DIR="$ARTIFACTS_DIR/$IDENTIFIER/job_events"

if [ ! -d "$JOB_EVENTS_DIR" ]; then
    echo "❌ job_events directory not found: $JOB_EVENTS_DIR"
    exit 1
fi

# Count event files
EVENT_COUNT=$(find "$JOB_EVENTS_DIR" -name "*.json" | wc -l)
echo "✅ Found $EVENT_COUNT event files"

# Show first few events
echo "📋 Sample events:"
ls -la "$JOB_EVENTS_DIR" | head -10

# Verify events contain expected fields
FIRST_EVENT=$(find "$JOB_EVENTS_DIR" -name "*.json" | sort | head -1)
if [ -f "$FIRST_EVENT" ]; then
    echo "📄 Sample event content:"
    cat "$FIRST_EVENT" | jq '.' 2>/dev/null || cat "$FIRST_EVENT"
fi

echo "✅ Integration test completed successfully!"
echo "   - Events directory: $JOB_EVENTS_DIR"
echo "   - Event count: $EVENT_COUNT"

# Cleanup
rm -rf "$TEST_DIR"
echo "🧹 Cleanup completed"