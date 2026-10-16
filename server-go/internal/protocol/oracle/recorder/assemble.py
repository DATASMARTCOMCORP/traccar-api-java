# Builds testdata/java-vectors.jsonl.gz: the recorded calls, with the SIR Suntech methods
# taken from the recording made with the pre-merge SuntechProtocolDecoder (907139a13^1).
#
# usage: python3 assemble.py vectors.jsonl sir-vectors.jsonl out.jsonl.gz
import gzip, json, sys

vectors, sir_vectors, out = sys.argv[1:4]
sir = {'testDecodeTwosComplement', 'testDecodeBrazilianCoordinates'}

def is_sir(r):
    return r.get('test') == 'SuntechProtocolDecoderTest' and r.get('method') in sir

lines = [l.rstrip('\n') for l in open(vectors) if not is_sir(json.loads(l))]
lines += [l.rstrip('\n') for l in open(sir_vectors) if is_sir(json.loads(l))]
with gzip.GzipFile(out, 'wb', mtime=0, compresslevel=9) as f:
    f.write(('\n'.join(lines) + '\n').encode())
print(len(lines), 'records')
