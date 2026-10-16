# Copies the Java protocol tests that call encoder.encodeCommand(...) directly, wrapping each
# call in recordEncode(encoder, command, <call>[, extra args]) on the same line, so the
# recorded line numbers match the original tests.
#
# usage: python3 rewrite_encoder_tests.py <repo>/src/test/java/org/traccar/protocol <out dir>
import glob, os, sys

src, out = sys.argv[1], sys.argv[2]
os.makedirs(out, exist_ok=True)
key = 'encoder.encodeCommand('
for f in glob.glob(os.path.join(src, '*.java')):
    text = open(f).read()
    if key not in text:
        continue
    parts, i = [], 0
    while True:
        j = text.find(key, i)
        if j < 0:
            parts.append(text[i:])
            break
        parts.append(text[i:j])
        k, depth = j + len(key), 1
        while depth:
            depth += {'(': 1, ')': -1}.get(text[k], 0)
            k += 1
        args = [a.strip() for a in text[j + len(key):k - 1].split(',')]
        if args in (['command'], ['null', 'command']):
            extra = ''
        elif args[0] == 'command':
            extra = ', ' + ', '.join(args[1:])
        else:
            raise SystemExit(f'{f}: unexpected arguments {args}')
        parts.append(f'recordEncode(encoder, command, {text[j:k]}{extra})')
        i = k
    open(os.path.join(out, os.path.basename(f)), 'w').write(''.join(parts))
