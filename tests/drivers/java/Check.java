// Driver interoperability check for ExecDB's pgwire (spec §8, phase 4
// Step 7): pgJDBC, used with its own default connection settings (no
// ExecDB-specific workaround flags -- notably preferQueryMode, which
// defaults to "extended", phase 4 Step 5), against a running ExecDB
// instance already seeded with table t(a INTEGER) (see tests/e2e.sh).
//
// JDBC's default auto-commit=true means no statement here runs inside an
// implicit transaction, unlike psycopg2's default (tests/drivers/python/
// check.py) -- so the DDL-rejection check below needs no explicit
// rollback to recover afterward.
import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.sql.Statement;

public class Check {
    public static void main(String[] args) throws Exception {
        if (args.length != 1) {
            System.err.println("usage: Check <jdbc-url>");
            System.exit(2);
        }
        try (Connection conn = DriverManager.getConnection(args[0])) {
            check(conn);
        }
        System.out.println("OK");
    }

    static void check(Connection conn) throws SQLException {
        try (PreparedStatement ps = conn.prepareStatement("SELECT ?")) {
            ps.setInt(1, 1);
            try (ResultSet rs = ps.executeQuery()) {
                rs.next();
                long one = rs.getLong(1);
                if (one != 1) {
                    throw new AssertionError("SELECT ?(1) returned " + one);
                }
            }
        }

        try (PreparedStatement ps = conn.prepareStatement("SELECT ?")) {
            ps.setDouble(1, 3.5);
            try (ResultSet rs = ps.executeQuery()) {
                rs.next();
                double f = rs.getDouble(1);
                if (f != 3.5) {
                    throw new AssertionError("SELECT ?(3.5) returned " + f);
                }
            }
        }

        try (PreparedStatement ps = conn.prepareStatement("SELECT ?")) {
            ps.setString(1, "hello");
            try (ResultSet rs = ps.executeQuery()) {
                rs.next();
                String s = rs.getString(1);
                if (!"hello".equals(s)) {
                    throw new AssertionError("SELECT ?('hello') returned " + s);
                }
            }
        }

        try (Statement st = conn.createStatement();
                ResultSet rs = st.executeQuery("SELECT x'00ff'")) {
            rs.next();
            byte[] blob = rs.getBytes(1);
            if (blob.length != 2 || blob[0] != 0x00 || (blob[1] & 0xFF) != 0xFF) {
                throw new AssertionError("SELECT x'00ff' returned an unexpected byte sequence");
            }
        }

        try (Statement st = conn.createStatement();
                ResultSet rs = st.executeQuery("SELECT NULL")) {
            rs.next();
            rs.getObject(1);
            if (!rs.wasNull()) {
                throw new AssertionError("SELECT NULL did not report wasNull()");
            }
        }

        // spec §2: DDL must be rejected via the external I/F, and pgJDBC
        // must surface it as an SQLException carrying SQLSTATE 42501
        // (spec §8), not just some generic failure.
        try (Statement st = conn.createStatement()) {
            st.execute("CREATE TABLE jdbc_should_not_exist(a INTEGER)");
            throw new AssertionError("expected CREATE TABLE to be rejected via the external I/F");
        } catch (SQLException e) {
            if (!"42501".equals(e.getSQLState())) {
                throw new AssertionError(
                        "expected SQLSTATE 42501, got " + e.getSQLState() + " (" + e.getMessage() + ")");
            }
        }

        // A basic write/read round trip against the table tests/e2e.sh seeded.
        try (PreparedStatement ps = conn.prepareStatement("INSERT INTO t VALUES (?)")) {
            ps.setInt(1, 777003);
            ps.executeUpdate();
        }
        try (PreparedStatement ps = conn.prepareStatement("SELECT count(*) FROM t WHERE a = ?")) {
            ps.setInt(1, 777003);
            try (ResultSet rs = ps.executeQuery()) {
                rs.next();
                long n = rs.getLong(1);
                if (n != 1) {
                    throw new AssertionError("expected count=1 after INSERT, got " + n);
                }
            }
        }
    }
}
