package org.traccar.storage;

import java.sql.Connection;
import java.sql.SQLException;

@FunctionalInterface
public interface SqlWork<R> {
    R run(Connection connection) throws SQLException;
}
