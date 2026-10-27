"""Fresh private local fixture builds. No profile, trust or real board is used."""
import argparse, hashlib, json, os, pathlib, shutil, socket, subprocess, time, uuid

REPO = pathlib.Path(__file__).resolve().parents[3]
ART = REPO.parent / 'artifacts' / '2026-10-25-slice-01'

def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()

def source_inputs():
    inputs = [p for root in ('cmd', 'internal', 'qualification/browser/localfixture')
              for p in (REPO / root).rglob('*') if p.is_file()
              and (p.suffix == '.go' or 'internal/assets/bundle/' in p.as_posix())]
    return sorted(inputs + [REPO / 'go.mod', REPO / 'go.sum'])

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--artifacts', type=pathlib.Path, default=ART)
    args = parser.parse_args()
    artifacts = args.artifacts.resolve(strict=True)
    run = artifacts / ('v02-local-' + time.strftime('%Y%m%d-%H%M%S-') + uuid.uuid4().hex[:8])
    run.mkdir(); (run / 'logs').mkdir(); (run / 'results').mkdir()
    result = {'status': 'STARTED', 'scope': 'local fixture preparation and mechanical tests only'}
    def save():
        (run / 'results' / 'preparation.json').write_text(json.dumps(result, indent=2) + '\n', encoding='utf-8')
    save()
    try:
        source = run / 'source'; source.mkdir()
        # Exact working-tree Go/module/assets inputs, including uncommitted fixes.
        before = {p.relative_to(REPO).as_posix(): digest(p) for p in source_inputs()}
        (run / 'source-before.json').write_text(json.dumps(before, indent=2) + '\n')
        for relative in before:
            target = source / relative; target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(REPO / relative, target)
        original = source / 'internal/transport/local/runtime.go'
        content = original.read_text(encoding='utf-8')
        needle = '&http.Server{Handler: r,'
        if content.count(needle) != 1:
            raise RuntimeError('HANDLER_BINDING_CHANGED')
        overlay = run / 'runtime-overlay.go'
        overlay.write_text(content.replace(needle, '&http.Server{Handler: v02QualificationWrap(r),') + '''
// Qualification-only hook: set before Start; normal source has no sentinel.
var v02QualificationWrap = func(h http.Handler) http.Handler { return h }
func V02QualificationDelivery(wrap func(http.Handler) http.Handler) { v02QualificationWrap = wrap }
''', encoding='utf-8', newline='\n')
        overlay_file = run / 'overlay.json'
        overlay_file.write_text(json.dumps({'Replace': {str(original): str(overlay)}}))
        env = os.environ.copy()
        env.update(GOTOOLCHAIN='local', CGO_ENABLED='0', GOOS='windows', GOARCH='amd64',
                   GOFLAGS='-mod=readonly', GOPROXY='off', GOSUMDB='off',
                   GOMODCACHE=str(artifacts / 'q01-cache/gomodcache'),
                   GOCACHE=str(run / 'cache'), GOTMPDIR=str(run / 'gotmp'),
                   V02_TEST_ROOT=str(run / 'mechanical-runtime'))
        (run / 'gotmp').mkdir(); (run / 'mechanical-runtime').mkdir()
        seed = artifacts / 'p02-integration-r2/cache'
        if seed.is_dir(): shutil.copytree(seed, run / 'cache')
        go = pathlib.Path('C:/Program Files/Go/bin/go.exe')
        pins = json.loads((REPO / 'qualification/build/results.json').read_text())
        if digest(go) != pins['tool_sha256']['go.exe']: raise RuntimeError('GO_HASH_MISMATCH')
        def execute(label, command, must_pass=True):
            completed = subprocess.run(command, cwd=source, env=env, capture_output=True)
            (run / 'logs' / (label + '.log')).write_bytes(completed.stdout + completed.stderr)
            if must_pass and completed.returncode: raise RuntimeError(label.upper() + '_FAILED')
            return completed
        if b'go1.27.3 windows/amd64' not in execute('go-version', [str(go), 'version']).stdout:
            raise RuntimeError('GO_VERSION_MISMATCH')
        common = [str(go), 'build', '-trimpath', '-buildvcs=false']
        execute('application-build', common + ['-o', str(run / 'localswim.exe'), './cmd/localswim'])
        env['L02_TEST_ROOT'] = str(run / 'mechanical-runtime')
        execute('ordinary-local-startup', [str(go), 'test', '-count=1', '-timeout=60s', '-run=^TestLocalLifecycleDurabilityAndRotation$', './internal/transport/local'])
        negative = execute('missing-overlay', common + ['-tags=v02qualification', '-o', str(run / 'negative.exe'), './qualification/browser/localfixture'], False)
        if negative.returncode == 0 or b'undefined: local.V02QualificationDelivery' not in negative.stderr:
            raise RuntimeError('MISSING_SENTINEL_GUARD_FAILED')
        execute('fixture-build', common + ['-tags=v02qualification', '-overlay', str(overlay_file), '-o', str(run / 'fixture.exe'), './qualification/browser/localfixture'])
        execute('mechanical-tests', [str(go), 'test', '-tags=v02qualification', '-overlay', str(overlay_file), '-count=1', '-timeout=60s', './qualification/browser/localfixture'])
        execute('fixture-vet', [str(go), 'vet', '-tags=v02qualification', '-overlay', str(overlay_file), './qualification/browser/localfixture'])
        after = {p.relative_to(REPO).as_posix(): digest(p) for p in source_inputs()}
        (run / 'source-after.json').write_text(json.dumps(after, indent=2) + '\n')
        if before != after: raise RuntimeError('SOURCE_CHANGED_DURING_BUILD')
        browser = json.loads((artifacts / 'v02-browser-pin-r1.json').read_text())
        chrome = pathlib.Path(browser['chromeExecutable']).resolve(strict=True)
        if digest(chrome) != browser['chromeSHA256']: raise RuntimeError('CHROME_HASH_MISMATCH')
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0)); port = sock.getsockname()[1]
        manifest = {'schemaVersion': 2, 'artifactDirectory': str(run), 'dataDirectory': str(run / 'runtime-data'),
                    'fixtureBinary': str(run / 'fixture.exe'), 'fixtureSHA256': digest(run / 'fixture.exe'),
                    'applicationBinary': str(run / 'localswim.exe'), 'applicationSHA256': digest(run / 'localswim.exe'),
                    'chromeExecutable': str(chrome), 'chromeSHA256': digest(chrome), 'browserVersion': browser['browserVersion'],
                    'browserArchiveSHA256': browser['browserArchiveSHA256'], 'playwrightVersion': browser['playwrightVersion'],
                    'port': port, 'evidenceId': run.name}
        (run / 'run-manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
        result.update(status='PREPARED_ONLY', manifest=str(run / 'run-manifest.json'),
                      sourceBeforeSHA256=digest(run / 'source-before.json'), sourceAfterSHA256=digest(run / 'source-after.json'),
                      overlaySHA256=digest(overlay), goSHA256=digest(go), missingOverlay='PASS', mechanics='PASS', vet='PASS',
                      evidenceLimit='Native controller mechanics only; no browser journey or power-loss qualification')
    except Exception as error:
        result.update(status='FAILED', errorCategory=type(error).__name__, failure=str(error) if isinstance(error, RuntimeError) else 'PREPARATION_UNAVAILABLE')
        save(); print(run); raise SystemExit(1)
    save(); print(run)

if __name__ == '__main__': main()
