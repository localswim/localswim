import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { inspectRaster } from '../src/api/raster-cover.ts';

const fixture = new URL('../../internal/coverimage/testdata/raster-admission.json', import.meta.url);
const vectors = JSON.parse(readFileSync(fixture, 'utf8'));

for (const { name, base64, zeroPaddedLength, header } of vectors) {
  test(name, () => {
    const decoded = Buffer.from(base64, 'base64');
    assert.equal(decoded.toString('base64'), base64);
    const length = zeroPaddedLength ?? decoded.length;
    assert.ok(length >= decoded.length);
    // Nonzero byteOffset exercises the same view semantics as the download bytes.
    const bytes = new Uint8Array(length + 2).subarray(1, length + 1);
    bytes.set(decoded);
    assert.deepEqual(inspectRaster(bytes), header);
  });
}
