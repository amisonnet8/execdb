// Driver interoperability check for ExecDB's pgwire (spec §8, phase 4
// Step 7): Npgsql, against a running ExecDB instance already seeded with
// table t(a INTEGER) (see tests/e2e.sh).
//
// Unlike the other three verified drivers (psycopg2, node-postgres,
// pgJDBC), Npgsql needs one connection-string opt-out to connect at all:
// "Server Compatibility Mode=NoTypeLoading" (supplied by the caller --
// tests/drivers/run-all.sh -- not hardcoded here). Without it, Npgsql's
// connection bootstrap sends a batch of SELECTs against Postgres system
// catalogs (pg_type, pg_enum, ...) plus a bare "SELECT version()" to
// build its type catalog; SQLite has none of those, so the very first
// connection attempt fails before this program's own code ever runs.
// This is a standard Npgsql feature for non-genuine-Postgres backends,
// not an ExecDB-specific patch.
//
// Npgsql always uses the Extended Query protocol (phase 4 Step 5) -- there
// is no simple-protocol fallback to opt out of, unlike pgx/pgJDBC -- which
// is exactly why phase 4's original plan deferred it: without Extended
// Query support, Npgsql could not connect to ExecDB at all. Like pgJDBC
// (tests/drivers/java/Check.java), Npgsql defaults to autocommit=true, so
// the DDL-rejection check below needs no explicit rollback to recover
// afterward.
using Npgsql;

if (args.Length != 1)
{
    Console.Error.WriteLine("usage: dotnet run -- <connectionString>");
    Environment.Exit(2);
}

await using var conn = new NpgsqlConnection(args[0]);
await conn.OpenAsync();
await Check(conn);
Console.WriteLine("OK");

static async Task Check(NpgsqlConnection conn)
{
    await using (var cmd = new NpgsqlCommand("SELECT @p", conn))
    {
        cmd.Parameters.AddWithValue("p", 1);
        var one = (long)(await cmd.ExecuteScalarAsync())!;
        if (one != 1)
        {
            throw new Exception($"SELECT @p (1) returned {one}");
        }
    }

    await using (var cmd = new NpgsqlCommand("SELECT @p", conn))
    {
        cmd.Parameters.AddWithValue("p", 3.5);
        var f = (double)(await cmd.ExecuteScalarAsync())!;
        if (f != 3.5)
        {
            throw new Exception($"SELECT @p (3.5) returned {f}");
        }
    }

    await using (var cmd = new NpgsqlCommand("SELECT @p", conn))
    {
        cmd.Parameters.AddWithValue("p", "hello");
        var s = (string)(await cmd.ExecuteScalarAsync())!;
        if (s != "hello")
        {
            throw new Exception($"SELECT @p ('hello') returned {s}");
        }
    }

    await using (var cmd = new NpgsqlCommand("SELECT x'00ff'", conn))
    {
        var blob = (byte[])(await cmd.ExecuteScalarAsync())!;
        if (blob.Length != 2 || blob[0] != 0x00 || blob[1] != 0xff)
        {
            throw new Exception("SELECT x'00ff' returned an unexpected byte sequence");
        }
    }

    await using (var cmd = new NpgsqlCommand("SELECT NULL", conn))
    {
        var n = await cmd.ExecuteScalarAsync();
        if (n != DBNull.Value)
        {
            throw new Exception($"SELECT NULL returned {n}");
        }
    }

    // spec §2: DDL must be rejected via the external I/F, and Npgsql must
    // surface it as a PostgresException carrying SQLSTATE 42501 (spec
    // §8), not just some generic failure.
    var rejected = false;
    try
    {
        await using var cmd = new NpgsqlCommand("CREATE TABLE dotnet_should_not_exist(a INTEGER)", conn);
        await cmd.ExecuteNonQueryAsync();
    }
    catch (PostgresException e)
    {
        rejected = true;
        if (e.SqlState != "42501")
        {
            throw new Exception($"expected SQLSTATE 42501, got {e.SqlState} ({e.Message})");
        }
    }
    if (!rejected)
    {
        throw new Exception("expected CREATE TABLE to be rejected via the external I/F");
    }

    // A basic write/read round trip against the table tests/e2e.sh seeded.
    await using (var cmd = new NpgsqlCommand("INSERT INTO t VALUES (@p)", conn))
    {
        cmd.Parameters.AddWithValue("p", 777004);
        await cmd.ExecuteNonQueryAsync();
    }
    await using (var cmd = new NpgsqlCommand("SELECT count(*) FROM t WHERE a = @p", conn))
    {
        cmd.Parameters.AddWithValue("p", 777004);
        var n = (long)(await cmd.ExecuteScalarAsync())!;
        if (n != 1)
        {
            throw new Exception($"expected count=1 after INSERT, got {n}");
        }
    }
}
