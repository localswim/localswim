import { spawn } from 'node:child_process';
import { createInterface } from 'node:readline';

// Only the owned child's bounded stdin/stdout NDJSON is a control path.
// Diagnostic bodies and credentials are deliberately not recorded.
export async function startFixture(run) {
  const child = spawn(run.fixtureBinary, ['--data', run.dataDirectory, '--port', String(run.port)], { windowsHide: true, stdio: ['pipe', 'pipe', 'pipe'] });
  let pending, ended = false, stderrBytes = 0, stdoutBytes = 0, serial = Promise.resolve();
  const exit = new Promise((resolve, reject) => {
    child.once('error', () => reject(Error('Fixture launch failed')));
    child.once('exit', (code, signal) => { ended = true; pending?.reject(Error('Fixture exited before response')); resolve({ code, signal }); });
  });
  exit.catch(() => {});
  child.stdin.on('error', () => pending?.reject(Error('Fixture input unavailable')));
  child.stderr.on('data', bytes => { stderrBytes += bytes.length; if (stderrBytes > 65_536) { child.stdin.end(); pending?.reject(Error('Fixture diagnostic bound exceeded')); } });
  const lines = createInterface({ input: child.stdout, crlfDelay: Infinity }), queue = [];
  lines.on('line', line => {
    stdoutBytes += Buffer.byteLength(line) + 1;
    if (Buffer.byteLength(line) > 65_536 || stdoutBytes > 2_000_000) { pending?.reject(Error('Fixture response bound exceeded')); child.stdin.end(); return; }
    let value;
    try { value = JSON.parse(line); } catch { pending?.reject(Error('Fixture protocol invalid')); return; }
    if (pending) { const waiter = pending; pending = undefined; waiter.resolve(value); } else queue.push(value);
  });
  const next = () => new Promise((resolve, reject) => {
    if (queue.length) return resolve(queue.shift());
    if (ended || pending) return reject(Error('Fixture protocol unavailable'));
    const timer = setTimeout(() => { pending = undefined; reject(Error('Fixture response timeout')); }, 40_000);
    pending = { resolve: value => { clearTimeout(timer); resolve(value); }, reject: error => { clearTimeout(timer); reject(error); } };
  });
  const verify = value => { if (!value || value.ok !== true) throw Error('Fixture control refused'); return value; };
  const call = message => {
    const job = serial.then(async () => { if (ended) throw Error('Fixture stopped'); child.stdin.write(JSON.stringify(message) + '\n'); return verify(await next()); });
    serial = job.catch(() => {}); return job;
  };
  const deadline = () => new Promise((_, reject) => { const timer = setTimeout(() => reject(Error('Fixture teardown timeout')), 40_000); timer.unref(); });
  try {
    const ready = verify(await next());
    if (ready.ready !== true || ready.origin !== run.origin || !['installationId', 'boardId', 'incarnation', 'epoch'].every(k => /^[a-f0-9-]{36}$/.test(ready[k]))) throw Error('Actual local fixture identity unavailable');
    return { ready, call, async close() {
      try { if (!ended) await call({ op: 'stop' }); } finally { child.stdin.end(); }
      const result = await Promise.race([exit, deadline()]); lines.close();
      if (result.code !== 0 || result.signal) throw Error('Fixture teardown failed');
      return { exitCode: result.code, stderrBytes };
    } };
  } catch (error) { child.stdin.end(); await Promise.race([exit, deadline()]).catch(() => {}); throw error; }
}
export const control = (fixture, message) => fixture.call(message);
