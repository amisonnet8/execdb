# Driver interoperability check for ExecDB's pgwire (spec §8, phase 4
# follow-up): Ruby's pg gem, against a running ExecDB instance already
# seeded with table t(a INTEGER) (see tests/e2e.sh).
#
# pg's exec_params sends real Extended Query parameter binding (Parse/
# Bind), unlike PHP's PDO_PGSQL default (tests/drivers/php/check.php) --
# even though both are, like psycopg2, libpq wrappers under the hood, this
# default usage pattern differs and is worth its own check.
require 'pg'

if ARGV.length != 1
  warn 'usage: check.rb <conninfo>'
  exit 2
end

conn = PG.connect(ARGV[0])

res = conn.exec_params('SELECT $1', [1])
one = res.values[0][0]
raise "SELECT $1 (1) returned #{one.inspect}" unless one.to_s == '1'

res = conn.exec_params('SELECT $1', [3.5])
f = res.values[0][0]
raise "SELECT $1 (3.5) returned #{f.inspect}" unless f.to_f == 3.5

res = conn.exec_params('SELECT $1', ['hello'])
s = res.values[0][0]
raise "SELECT $1 ('hello') returned #{s.inspect}" unless s == 'hello'

# result_format 1 (binary): pg's default text format returns bytea as
# the literal "\x00ff"-style hex-escape string, not decoded into raw
# bytes -- unlike psycopg2, which decodes it automatically even in text
# mode (tests/drivers/python/check.py). Requesting binary here checks
# ExecDB's binary bytea encoding instead of re-deriving pg's own text
# escaping.
res = conn.exec_params("SELECT x'00ff'", [], 1)
blob = res.values[0][0]
raise "SELECT x'00ff' returned #{blob.inspect}" unless blob == "\x00\xff".b

res = conn.exec('SELECT NULL')
null = res.values[0][0]
raise "SELECT NULL returned #{null.inspect}" unless null.nil?

# spec §2: DDL must be rejected via the external I/F, and pg must surface
# it as a PG::Error carrying SQLSTATE 42501 (spec §8).
begin
  conn.exec('CREATE TABLE ruby_should_not_exist(a INTEGER)')
  raise 'expected CREATE TABLE to be rejected via the external I/F'
rescue PG::Error => e
  code = e.result.error_field(PG::PG_DIAG_SQLSTATE)
  raise "expected SQLSTATE 42501, got #{code.inspect} (#{e.message})" unless code == '42501'
end

# A basic write/read round trip against the table tests/e2e.sh seeded.
conn.exec_params('INSERT INTO t VALUES ($1)', [777010])
res = conn.exec_params('SELECT count(*) FROM t WHERE a = $1', [777010])
n = res.values[0][0].to_i
raise "expected count=1 after INSERT, got #{n}" unless n == 1

puts 'OK'
