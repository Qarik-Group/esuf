#!/bin/bash
set -e

# build.sh - Build wrapper
#
#  - changes build id from "master" to the commitish of HEAD
#  - detects if there are uncommitted local changes
#  - runs go build to build a esuf binary
#  - undoes the change (so that it doesn't get committed)

function auto_sed() {
	cmd=$1
	shift

	if [[ "$(uname -s)" == "Darwin" ]]; then
		sed -i '' -e "$cmd" $@
	else
		sed -i -e "$cmd" $@
	fi
}


# track dirty changes in the local working copy
if [[ $(git status --porcelain) != "" ]]; then
	auto_sed "s/var DIRTY = \".*\"/var DIRTY = \" with uncommitted local changes\"/" main.go
fi

# update BUILD to be the HEAD commit-ish
sha1=$(git rev-list --abbrev-commit HEAD -n1)
if [[ -z ${IN_RELEASE} ]]; then
	auto_sed "s/var BUILD = \".*\"/var BUILD = \"${sha1}\"/" main.go
else
	auto_sed "s/var BUILD = \".*\"/var BUILD = \"release\"/" main.go
fi

# do the build
if [[ -n ${IN_RELEASE} ]]; then
	mkdir -p releases
	gox -osarch="linux/amd64 linux/386 darwin/amd64" --output="${OUTPUT:-releases}/esuf-{{.OS}}-{{.Arch}}"
elif [[ -n ${1} ]]; then
	# allow usage like `./build.sh linux/amd64`
	gox -osarch="$*" --output="esuf-{{.OS}}-{{.Arch}}"
fi

go build .

# put it all back
auto_sed "s/var BUILD = \".*\"/var BUILD = \"master\"/" main.go
auto_sed "s/var DIRTY = \".*\"/var DIRTY = \"\"/" main.go

# what version?
./esuf -v
