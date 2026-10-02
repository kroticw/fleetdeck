#!/bin/sh

# Prints only the selected directory to stdout, so `make fleetdeck` can pass it
# straight to window-app. Prompts go to stderr and remain visible while stdout is
# captured by make.
set -eu

if [ "$#" -ne 1 ]; then
	echo "usage: $0 <project-dir>" >&2
	exit 2
fi

project_dir=$1

while :; do
	printf '%s\n' \
		'Where should fleetdeck.app be installed?' \
		'  1) fleetdeck/bin/ (default)' \
		'  2) ~/Applications/' \
		'  3) Enter another directory' >&2
	printf 'Choice [1]: ' >&2
	IFS= read -r choice || choice=

	case "$choice" in
	'' | 1)
		printf '%s\n' "$project_dir/bin"
		exit 0
		;;
	2)
		printf '%s\n' "$HOME/Applications"
		exit 0
		;;
	3)
		printf 'Directory: ' >&2
		IFS= read -r directory || directory=
		case "$directory" in
		'')
			echo 'Directory cannot be empty.' >&2
			;;
		'~')
			printf '%s\n' "$HOME"
			exit 0
			;;
		'~/'*)
			printf '%s/%s\n' "$HOME" "${directory#\~/}"
			exit 0
			;;
		*)
			printf '%s\n' "$directory"
			exit 0
			;;
		esac
		;;
	*)
		echo 'Choose 1, 2, or 3.' >&2
		;;
	esac
done
