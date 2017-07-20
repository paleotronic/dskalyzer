#!/bin/bash

ARCHES="darwin-amd64 windows-386 windows-amd64 linux-386 linux-amd64 linux-arm freebsd-arm freebsd-amd64 freebsd-386"
BINDIR="bin"

mkdir -p "$BINDIR"

for arch in `echo $ARCHES`; do
	export GOOS=`echo $arch | awk -F"-" '{print $1}'`
	export GOARCH=`echo $arch | awk -F"-" '{print $2}'`
	EXENAME="dskalyzer-$GOOS-$GOARCH"
	if [ "$GOOS" == "windows" ]; then
		EXENAME="$EXENAME.exe"
	fi
	echo "Building $BINDIR/$EXENAME..."
	go build -o "$BINDIR/$EXENAME" .
done
