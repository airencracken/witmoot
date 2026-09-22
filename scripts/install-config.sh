#!/bin/sh
# Preserve local configuration, including dangling symlinks managed elsewhere.
if [ "$#" -ne 3 ]; then
	echo 'Usage: install-config.sh SOURCE DESTINATION MODE' >&2
	exit 1
fi
if [ -e "$2" ] || [ -L "$2" ]; then
	printf 'Keeping existing configuration: %s\n' "$2"
else
	install -m "$3" "$1" "$2" || exit 1
fi
