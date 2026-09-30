'use strict';

const assert = require('node:assert/strict');
const test = require('node:test');
const { resolveGitHubPermission } = require('./github-permission.cjs');

const maintainFlags = {
  admin: false,
  maintain: true,
  push: true,
  triage: true,
  pull: true,
};

test('keeps built-in roles', () => {
  assert.equal(resolveGitHubPermission({ role_name: 'write' }), 'write');
});

test('resolves a custom role from compatible effective signals', () => {
  assert.equal(resolveGitHubPermission({
    permission: 'write',
    role_name: 'ODH Repo Maintainer',
    user: { permissions: maintainFlags },
  }), 'maintain');
});

test('resolves a custom role from precise signals alone', () => {
  assert.equal(resolveGitHubPermission({
    role_name: 'ODH Repo Maintainer',
    user: { permissions: maintainFlags },
  }), 'maintain');
});

test('uses the conservative legacy fallback', () => {
  assert.equal(resolveGitHubPermission({ permission: 'read', role_name: 'Custom Triage' }), 'read');
});

for (const [name, payload] of [
  ['missing signals', { role_name: 'Custom' }],
  ['null flags', { permission: 'write', role_name: 'Custom', user: { permissions: null } }],
  ['malformed user', { permission: 'write', role_name: 'Custom', user: null }],
  ['conflicting signals', {
    permission: 'read',
    role_name: 'Custom',
    user: { permissions: { ...maintainFlags, maintain: false } },
  }],
  ['contradictory hierarchy', {
    permission: 'write',
    role_name: 'Custom',
    user: { permissions: { ...maintainFlags, push: false } },
  }],
]) {
  test(`rejects ${name}`, () => {
    assert.throws(() => resolveGitHubPermission(payload));
  });
}

for (const permission of [42, false, true, {}, []]) {
  test(`rejects non-string permission ${JSON.stringify(permission)}`, () => {
    assert.throws(() => resolveGitHubPermission({ role_name: 'write', permission }));
  });
}
