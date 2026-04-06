#!/bin/bash

# Navigate to the project directory
cd "$(dirname "$0")/.."

# Build the application
go build -o lumenvec ./cmd/server

# Run the application
./lumenvec
