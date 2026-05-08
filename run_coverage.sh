#!/bin/bash
# Run coverage and show summary
go test -coverpkg=./... -coverprofile=coverage.out -covermode=atomic ./... 2>&1
echo "=== COVERAGE SUMMARY ==="
go tool cover -func=coverage.out 2>/dev/null | grep -v "100%" | head -50
echo "=== TOTAL COVERAGE ==="
go tool cover -func=coverage.out 2>/dev/null | tail -1
rm -f coverage.out