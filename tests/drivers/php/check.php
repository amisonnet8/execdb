<?php
// Driver interoperability check for ExecDB's pgwire (spec §8, phase 4
// follow-up): PHP's PDO_PGSQL (via PDO), against a running ExecDB
// instance already seeded with table t(a INTEGER) (see tests/e2e.sh).
//
// PDO_PGSQL defaults to "emulated prepares" (PDO::ATTR_EMULATE_PREPARES
// is true by default for this driver): parameter values are substituted
// into the SQL text on the client side and the whole statement is sent
// as plain text via Simple Query, not Extended Query's Parse/Bind. This
// is a genuinely different code path from every other verified driver
// (all of which use real Extended Query parameter binding by default),
// even though PDO_PGSQL, like psycopg2, is itself a libpq wrapper --
// worth keeping even though libpq's own wire handling was already
// exercised by psycopg2, precisely because this default differs.

if ($argc !== 2) {
    fwrite(STDERR, "usage: check.php <dsn>\n");
    exit(2);
}

$dsn = $argv[1];
$pdo = new PDO($dsn);
$pdo->setAttribute(PDO::ATTR_ERRMODE, PDO::ERRMODE_EXCEPTION);

$stmt = $pdo->prepare("SELECT ?");
$stmt->execute([1]);
$one = $stmt->fetchColumn();
if ((string)$one !== "1") {
    fwrite(STDERR, "SELECT ? (1) returned " . var_export($one, true) . "\n");
    exit(1);
}

$stmt = $pdo->prepare("SELECT ?");
$stmt->execute([3.5]);
$f = $stmt->fetchColumn();
if ((float)$f !== 3.5) {
    fwrite(STDERR, "SELECT ? (3.5) returned " . var_export($f, true) . "\n");
    exit(1);
}

$stmt = $pdo->prepare("SELECT ?");
$stmt->execute(["hello"]);
$s = $stmt->fetchColumn();
if ($s !== "hello") {
    fwrite(STDERR, "SELECT ? ('hello') returned " . var_export($s, true) . "\n");
    exit(1);
}

$stmt = $pdo->query("SELECT NULL");
$null = $stmt->fetchColumn();
if ($null !== null) {
    fwrite(STDERR, "SELECT NULL returned " . var_export($null, true) . "\n");
    exit(1);
}

// spec §2: DDL must be rejected via the external I/F, and PDO must
// surface it as a PDOException carrying SQLSTATE 42501 (spec §8).
try {
    $pdo->exec("CREATE TABLE php_should_not_exist(a INTEGER)");
    fwrite(STDERR, "expected CREATE TABLE to be rejected via the external I/F\n");
    exit(1);
} catch (PDOException $e) {
    if ($e->getCode() !== "42501") {
        fwrite(STDERR, "expected SQLSTATE 42501, got " . var_export($e->getCode(), true) . " (" . $e->getMessage() . ")\n");
        exit(1);
    }
}

// A basic write/read round trip against the table tests/e2e.sh seeded.
$stmt = $pdo->prepare("INSERT INTO t VALUES (?)");
$stmt->execute([777009]);
$stmt = $pdo->prepare("SELECT count(*) FROM t WHERE a = ?");
$stmt->execute([777009]);
$n = $stmt->fetchColumn();
if ((int)$n !== 1) {
    fwrite(STDERR, "expected count=1 after INSERT, got " . var_export($n, true) . "\n");
    exit(1);
}

echo "OK\n";
