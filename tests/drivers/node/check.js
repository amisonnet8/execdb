// Driver interoperability check for ExecDB's pgwire (spec §8, phase 4
// Step 7): node-postgres (pg), used with its own default connection
// settings (no ExecDB-specific workaround flags), against a running
// ExecDB instance already seeded with table t(a INTEGER) (see
// tests/e2e.sh).
//
// The parameterized queries below ($1) make pg send Parse/Bind/Describe/
// Execute/Sync (Extended Query, phase 4 Step 5) rather than a plain Query
// message, exercising that path from a second driver besides pgx
// (tests/pgclient already covers pgx's default Extended Query mode).
'use strict';

const { Client } = require('pg');

async function main() {
  const dsn = process.argv[2];
  if (!dsn) {
    console.error('usage: node check.js <connectionString>');
    process.exit(2);
  }

  const client = new Client({ connectionString: dsn });
  await client.connect();
  try {
    await check(client);
  } finally {
    await client.end();
  }
  console.log('OK');
}

function firstValue(result) {
  return Object.values(result.rows[0])[0];
}

async function check(client) {
  let res = await client.query('SELECT $1', [1]);
  const one = firstValue(res);
  // int8 (OID 20, ExecDB's mapping for an INTEGER-affinity column,
  // cmd/execdb/pgtype.go) is returned by pg as a string by default --
  // JS's Number can't safely represent the full int8 range -- so compare
  // as a string rather than assuming a JS number.
  if (String(one) !== '1') {
    throw new Error(`SELECT $1 (1) returned ${JSON.stringify(one)}`);
  }

  res = await client.query('SELECT $1', [3.5]);
  const f = firstValue(res);
  if (Number(f) !== 3.5) {
    throw new Error(`SELECT $1 (3.5) returned ${JSON.stringify(f)}`);
  }

  res = await client.query('SELECT $1', ['hello']);
  const s = firstValue(res);
  if (s !== 'hello') {
    throw new Error(`SELECT $1 ('hello') returned ${JSON.stringify(s)}`);
  }

  res = await client.query("SELECT x'00ff'");
  const blob = firstValue(res);
  if (!Buffer.isBuffer(blob) || !blob.equals(Buffer.from([0x00, 0xff]))) {
    throw new Error(`SELECT x'00ff' returned ${JSON.stringify(blob)}`);
  }

  res = await client.query('SELECT NULL');
  const nullVal = firstValue(res);
  if (nullVal !== null) {
    throw new Error(`SELECT NULL returned ${JSON.stringify(nullVal)}`);
  }

  // spec §2: DDL must be rejected via the external I/F, and pg must
  // surface it as an error carrying SQLSTATE 42501 (spec §8).
  let rejected = false;
  try {
    await client.query('CREATE TABLE node_should_not_exist(a INTEGER)');
  } catch (e) {
    rejected = true;
    if (e.code !== '42501') {
      throw new Error(`expected SQLSTATE 42501 for the rejected CREATE TABLE, got ${e.code} (${e.message})`);
    }
  }
  if (!rejected) {
    throw new Error('expected CREATE TABLE to be rejected via the external I/F');
  }

  // A basic write/read round trip against the table tests/e2e.sh seeded.
  await client.query('INSERT INTO t VALUES ($1)', [777002]);
  res = await client.query('SELECT count(*) FROM t WHERE a = $1', [777002]);
  const n = Number(firstValue(res));
  if (n !== 1) {
    throw new Error(`expected count=1 after INSERT, got ${n}`);
  }
}

main().catch((err) => {
  console.error('Error:', err.message || err);
  process.exit(1);
});
