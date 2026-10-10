#!/bin/sh
# A command hook: read the event JSON and refuse destructive rm calls.
event=$(cat)
case "$event" in
	*"rm -rf"*) printf '{"block":true,"reason":"rm -rf is blocked by the block-rm example"}' ;;
	*) printf '{}' ;;
esac
