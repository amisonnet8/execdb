# CI/CD: an instant test database

*日本語版はこちら: [ci-testing_ja.md](ci-testing_ja.md)*

The idea: build one executable that already has your schema and seed data
baked in, then have every test job just run it — no database container, no
migration step, no waiting for a service to become healthy.

## 1. Build a seeded snapshot once

Grab the binary for your platform from the
[latest release](https://github.com/amisonnet8/execdb/releases/latest) (no
Go required), or `go install github.com/amisonnet8/execdb/cmd/execdb@latest`
if you have Go 1.26+. Then:

```sh
execdb <<'SQL'
CREATE TABLE users(id INTEGER PRIMARY KEY, name TEXT NOT NULL, email TEXT UNIQUE);
CREATE TABLE posts(id INTEGER PRIMARY KEY, user_id INTEGER REFERENCES users(id), title TEXT);
INSERT INTO users(name, email) VALUES ('Alice', 'alice@example.com'), ('Bob', 'bob@example.com');
INSERT INTO posts(user_id, title) VALUES (1, 'Hello World'), (1, 'Second post'), (2, 'Bob''s post');
.snapshot testdb
.exit
SQL
```

This produces `testdb`, a standalone executable — commit it to your test
fixtures directory, or build it as a CI artifact step that later jobs
download. (Don't commit ExecDB binaries to a *source* repository as a
matter of habit — this is a test fixture, which is a different case; see
[`.claude/rules/distribution.md`](../../.claude/rules/distribution.md) for
why release binaries themselves aren't committed.)

## 2. Use it in a test job

Run it in server mode and point your application's `DATABASE_URL` at it —
no separate container, no wait-for-healthcheck step:

```sh
chmod +x testdb
./testdb -n -p 127.0.0.1:5432 -q &
SERVER_PID=$!

# your test suite, e.g.:
DATABASE_URL="postgres://any@127.0.0.1:5432/any" go test ./...

kill "$SERVER_PID"
```

Since data lives in memory and each run starts from the same snapshot, every
job gets an identical, pristine database with zero setup time — the whole
"start a Postgres container and wait for it" step disappears. See
[Connecting from your application](mock-server.md) for driver-specific
connection examples.

## Example: GitHub Actions

```yaml
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - name: Start test database
        run: |
          chmod +x fixtures/testdb
          ./fixtures/testdb -n -p 127.0.0.1:5432 -q &
      - name: Run tests
        env:
          DATABASE_URL: postgres://any@127.0.0.1:5432/any
        run: go test ./...
```

No `services:` block, no image pull, no port-wait loop — the database is
just another step in the job.
