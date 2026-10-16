# Java protocol test vectors

`testdata/java-vectors.jsonl.gz` records every call the Java protocol tests make
(`src/test/java/org/traccar/protocol`, 455 tests): each decoder call with its input and full
output, each frame decoder check, and each command encoding. One JSON object per line:

- `decode`: `decoder`, `instance` (one Java decoder object), `input` (`bytes` hex, `string`,
  or `http` method, URI, headers and body hex), `output` (`position` or `positions`, with
  doubles as raw IEEE bits and times as epoch milliseconds, attributes as Jackson writes them
  plus each attribute's Java type, the network JSON) or `error` (the exception).
- `frame` / `frameNull`: the buffers the test built (`created`, the input last) and the frame.
- `command` (`verifyCommand`) and `encode` (`encodeCommand`, `encoder.encodeCommand(...)`):
  the command JSON and the output (`bytes` hex or `text`).
- `test`, `method`, `line`: where in the Java test; `now`: the clock at the call.

## Recording

The recorder is a copy of `src/test/java/org/traccar/ProtocolTest.java` whose verify methods
also write these records, plus copies of the encoder tests with `encoder.encodeCommand(...)`
wrapped in `recordEncode(...)` (same line numbers). It runs against the compiled test classes
(`./gradlew compileTestJava`) with the JUnit launcher, under
`TZ=America/Sao_Paulo -Duser.timezone=America/Sao_Paulo -Doracle.out=<file>`. Nothing in
`src/` changes.

The SIR Suntech methods (`testDecodeTwosComplement`, `testDecodeBrazilianCoordinates`) are
recorded with `SuntechProtocolDecoder` from `907139a13^1` (the SIR fix, MIGRATION.md §8), since
the current decoder fails them.
