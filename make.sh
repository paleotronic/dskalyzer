#!/bin/bash

ARCHES="darwin-amd64 windows-386 windows-amd64 linux-386 linux-amd64 linux-arm freebsd-arm freebsd-amd64 freebsd-386"
BINDIR="bin"
PUBLISH="publish"

mkdir -p "$BINDIR"
mkdir -p "$PUBLISH"

go get github.com/chzyer/readline

exitState=0
for arch in `echo $ARCHES`; do
	export GOOS=`echo $arch | awk -F"-" '{print $1}'`
	export GOARCH=`echo $arch | awk -F"-" '{print $2}'`
	EXENAME="$BINDIR/dskalyzer-$GOOS-$GOARCH"
	ZIPNAME="$PUBLISH/dskalyzer-$GOOS-$GOARCH.zip"
	if [ "$GOOS" == "windows" ]; then
		EXENAME="$EXENAME.exe"
	fi
	echo "Building $EXENAME..."
	go build -o "$EXENAME" .
	if [ "$?" == "0" ]; then
		echo "Zipping -> $ZIPNAME"
		zip "$ZIPNAME" "$EXENAME" "LICENSE" "README.md"
	else
		exit 2
	fi
done
