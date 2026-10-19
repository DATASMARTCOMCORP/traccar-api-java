import os, pathlib, subprocess, tempfile
script = str(pathlib.Path(__file__).resolve().with_name('deploy-production.sh'))
for mode in ('success', 'health', 'install'):
    failure = mode != 'success'
    with tempfile.TemporaryDirectory(prefix='sir-tracker-deploy-test-') as tmp:
        root = pathlib.Path(tmp)
        app, stage, mocks = (root / s for s in ('app', 'stage', 'mocks'))
        for directory in (app, stage, mocks):
            directory.mkdir()
        for directory, value in ((app, 'old'), (stage, 'new')):
            (directory / 'tracker-server.jar').write_text(value)
            for sub in ('lib', 'schema', 'templates'):
                (directory / sub).mkdir()
                (directory / sub / 'fixture.jar').write_text(value)
        (app / 'conf').mkdir()
        (app / 'conf' / 'sir-prd.xml').write_text('preserve-config')
        (app / 'sir-web').mkdir()
        (app / 'sir-web' / 'index.html').write_text('preserve-web')
        (mocks / 'systemctl').write_text('#!/bin/bash\nprintf "%s\\n" "$*" >> "$MOCK_APP/systemctl.log"\nexit 0\n')
        (mocks / 'sleep').write_text('#!/bin/bash\nexit 0\n')
        (mocks / 'df').write_text('#!/bin/bash\nprintf "Filesystem 1024-blocks Used Available Capacity Mounted\\nmock 10000000 0 9000000 0%% /\\n"\n')
        (mocks / 'curl').write_text('''#!/bin/bash
if [[ "$MOCK_FAIL" == 1 && "$(cat "$MOCK_APP/tracker-server.jar")" == new ]]; then exit 1; fi
if [[ "$*" == *position-mobile/batch* ]]; then printf '{"accepted":0,"error":"invalid batch"}\\n400'; fi
''')
        (mocks / 'mv').write_text('''#!/bin/bash
if [[ "$MOCK_MODE" == install && "$*" == *incoming-aaaaaaaaaaaa-lib* ]]; then exit 1; fi
exec /usr/bin/mv "$@"
''')
        for command in mocks.iterdir():
            command.chmod(0o755)
        env = dict(os.environ, PATH=str(mocks)+':'+os.environ['PATH'], MOCK_APP=str(app), MOCK_FAIL=str(int(mode == 'health')), MOCK_MODE=mode)
        result = subprocess.run(['bash', script, str(stage), str(app), 'sir-tracker', 'conf/sir-prd.xml', 'a'*40], env=env, text=True, capture_output=True)
        assert (result.returncode != 0) == failure, result.stdout+result.stderr
        expected = 'old' if failure else 'new'
        assert (app / 'tracker-server.jar').read_text() == expected
        for sub in ('lib', 'schema', 'templates'):
            assert (app / sub / 'fixture.jar').read_text() == expected
        assert (app / 'conf' / 'sir-prd.xml').read_text() == 'preserve-config'
        assert (app / 'sir-web' / 'index.html').read_text() == 'preserve-web'
        assert list((app / '.deploy-backups').glob('*/tracker-server.jar'))[0].read_text() == 'old'
        if failure:
            assert 'Previous service health restored: true' in result.stderr
        assert not list(app.glob('.incoming-*'))
        print((mode + ' scenario') + ': PASS; config and frontend preserved')
