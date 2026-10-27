import { connect } from 'node:net';

// Only the ACL-bound qualification pipe carries short-lived code material.
// Never print a request/response or include it in an assertion diagnostic.
export function control(run, message) {
  return new Promise((resolve, reject) => {
    let done = false, bytes = Buffer.alloc(0);
    const socket = connect(run.pipeName);
    const finish = (error, value) => {
      if (done) return;
      done = true; socket.destroy();
      if (error) reject(Error('Private fixture control unavailable'));
      else resolve(value);
    };
    socket.setTimeout(35_000, () => finish(true));
    socket.on('error', () => finish(true));
    socket.on('connect', () => socket.write(JSON.stringify(message) + '\n'));
    socket.on('data', part => {
      bytes = Buffer.concat([bytes, part]);
      if (bytes.length > 65_536) return finish(true);
      const end = bytes.indexOf(10);
      if (end < 0) return;
      try {
        const value = JSON.parse(bytes.subarray(0, end).toString('utf8'));
        bytes.fill(0);
        if (value.error || value.ok === false) return finish(true);
        finish(false, value);
      } catch { finish(true); }
    });
    socket.on('end', () => { if (!done) finish(true); });
  });
}

export async function requireReady(run) {
  const status = await control(run, { op: 'status' });
  if (status.installationId !== run.installationId || status.origin !== run.origin || status.boardId !== run.boardId || status.incarnation !== run.incarnation ||
      status.trustInstalled !== true || status.humanBootstrapConfirmed !== true || status.ownerDelegationConfirmed !== true || status.ready !== true) {
    throw Error('Actual trust, human bootstrap and scoped owner delegation are required');
  }
  return status;
}
