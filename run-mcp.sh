#!/bin/bash
cd /Users/adhameldeeb/dev/open_trace
set -a
source .env
set +a
exec ./opentrace mcp
