#!/bin/sh
# Regenerates testdata/renders.jsonl.gz from the Java engine (see Record.java). Run from
# this directory after ./gradlew assemble at the repository root.
set -e
root=$(cd ../../../.. && pwd)
tmp=$(mktemp -d)
javac -d "$tmp" -cp "$root/target/tracker-server.jar" Record.java
LANG=C.UTF-8 TZ=America/Sao_Paulo java -cp "$root/target/tracker-server.jar:$tmp" Record \
    "$root/templates" ../testdata/prod ../testdata/cases.json 2>/dev/null | gzip -9 > ../testdata/renders.jsonl.gz
rm -rf "$tmp"
