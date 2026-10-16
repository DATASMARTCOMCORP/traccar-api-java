#!/bin/bash
# Records the Java protocol tests' calls into ../testdata/java-vectors.jsonl.gz.
# Needs Java 21 and the repository's Gradle build; writes only under $WORK.
#
# usage: record.sh <repository root> <work dir>
set -euo pipefail
REPO=$(cd "$1" && pwd)
WORK=$(mkdir -p "$2" && cd "$2" && pwd)
HERE=$(cd "$(dirname "$0")" && pwd)

cd "$REPO"
./gradlew -q compileTestJava
CP=$(./gradlew -q --init-script "$HERE/classpath.gradle" printTestClasspath | sed -n 's/^CP=//p')

run() { # <classes dir> <out file> [test classes...]
    local classes=$1 out=$2; shift 2
    rm -f "$out"
    env -i PATH="$PATH" HOME="$HOME" LANG=C.UTF-8 TZ=America/Sao_Paulo \
        java -Duser.timezone=America/Sao_Paulo -Doracle.out="$out" -cp "$classes:$CP" RunTests "$@"
}

# The recorder, the rewritten encoder tests and the runner, ahead of the test classes.
python3 "$HERE/rewrite_encoder_tests.py" src/test/java/org/traccar/protocol "$WORK/src/org/traccar/protocol"
cp "$HERE/ProtocolTest.java" "$WORK/src/org/traccar/ProtocolTest.java"
rm -rf "$WORK/classes" && mkdir -p "$WORK/classes"
javac -nowarn -cp "$CP" -d "$WORK/classes" "$WORK"/src/org/traccar/ProtocolTest.java \
    "$WORK"/src/org/traccar/protocol/*.java "$HERE/RunTests.java"
run "$WORK/classes" "$WORK/vectors.jsonl" || true # the SIR methods fail on the current decoder

# The SIR Suntech methods, with the decoder from before the upstream merge.
mkdir -p "$WORK/sir/src/org/traccar/protocol" "$WORK/sir/classes"
git show '907139a13^1:src/main/java/org/traccar/protocol/SuntechProtocolDecoder.java' \
    > "$WORK/sir/src/org/traccar/protocol/SuntechProtocolDecoder.java"
javac -nowarn -cp "$CP" -d "$WORK/sir/classes" "$WORK/sir/src/org/traccar/protocol/SuntechProtocolDecoder.java"
run "$WORK/sir/classes:$WORK/classes" "$WORK/sir-vectors.jsonl" SuntechProtocolDecoderTest || true

python3 "$HERE/assemble.py" "$WORK/vectors.jsonl" "$WORK/sir-vectors.jsonl" "$HERE/../testdata/java-vectors.jsonl.gz"
