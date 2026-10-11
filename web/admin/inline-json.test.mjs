import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import vm from 'node:vm';
const source = await readFile(new URL('core.js', import.meta.url), 'utf8');
const body = source.slice(source.indexOf('function inlineJSON('), source.indexOf('\nfunction fmtTime'));
const encode = vm.runInNewContext(`${body}; inlineJSON`);
test('inline JSON quotes are safe in HTML attributes and preserve values', () => {
  for (const value of ['member-123', 'Alice "A" & Bob\'s <team>', ['a', 'b'], null]) {
    const encoded = encode(value);
    assert.equal(/["'<>]/.test(encoded), false);
    const decoded = encoded.replace(/&quot;/g, '"').replace(/&#39;/g, "'")
      .replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&amp;/g, '&');
    assert.deepEqual(JSON.parse(decoded), value);
  }
});
