#!/bin/sh -eux

OSes="linux freebsd dragonflybsd openbsd netbsd android darwin solaris plan9"
ARCHs="386 amd64 arm arm64 ppc64 mips mips64"

for GOOS in $OSes; do
    export GOOS
    for GOARCH in $ARCHs; do
        export GOARCH
        go build -o "./eget2_${GOOS}_${GOARCH}" || break
        strip -sx "./eget2_${GOOS}_${GOARCH}"
        cp "./eget2_${GOOS}_${GOARCH}" "./eget2_${GOOS}_${GOARCH}.upx"
        upx "./eget2_${GOOS}_${GOARCH}.upx" || rm "./eget2_${GOOS}_${GOARCH}.upx"
    done
done
