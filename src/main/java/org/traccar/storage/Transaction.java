package org.traccar.storage;

import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.util.ArrayList;
import java.util.List;

/**
 * The unit of work for one position: it starts by locking the device and ends with a single commit, so the
 * position, its receipt, the device state and the events it causes are stored together or not at all. Work
 * that leaves the process (WebSocket pushes, notifications, webhooks) is registered with {@link #afterCommit}
 * and runs only once the commit succeeded.
 *
 * <p>This base class is the storage-without-transactions case: nothing to commit, and after-commit work runs
 * when the unit finishes.
 */
public class Transaction {

    private static final Logger LOGGER = LoggerFactory.getLogger(Transaction.class);

    public static final String COMMITTED = "committed";
    public static final String ROLLED_BACK = "rolled_back";
    public static final String COMMIT_FAILED = "commit_failed";

    private final long startNanos = System.nanoTime();
    private final List<Runnable> afterCommit = new ArrayList<>();
    private boolean rollbackOnly;
    private String result;
    private long commitNanos = -1;

    /** Restores the previous binding of the thread when closed. */
    public interface Scope extends AutoCloseable {
        @Override
        void close();
    }

    /**
     * Makes storage calls of the current thread part of this transaction until the scope closes. Scopes nest,
     * since the processing chain may continue inline from inside a handler.
     */
    public Scope bind() {
        return () -> { };
    }

    public void afterCommit(Runnable action) {
        afterCommit.add(action);
    }

    /** Marks the transaction failed: {@link #finish} rolls it back. */
    public void setRollbackOnly() {
        rollbackOnly = true;
    }

    public boolean isRollbackOnly() {
        return rollbackOnly;
    }

    /**
     * Commits, or rolls back when marked rollback-only, then runs the after-commit work if it committed.
     *
     * @return whether the work is durably stored
     */
    public boolean finish() {
        if (rollbackOnly) {
            rollback();
            result = ROLLED_BACK;
            return false;
        }
        long start = System.nanoTime();
        try {
            commit();
        } catch (StorageException e) {
            commitNanos = System.nanoTime() - start;
            LOGGER.warn("Transaction commit failed", e);
            rollback();
            result = COMMIT_FAILED;
            return false;
        }
        commitNanos = System.nanoTime() - start;
        result = COMMITTED;
        for (Runnable action : afterCommit) {
            try {
                action.run();
            } catch (RuntimeException e) {
                LOGGER.warn("After-commit action failed", e);
            }
        }
        return true;
    }

    /** {@link System#nanoTime} when the transaction started, its device locked. */
    public long getStartNanos() {
        return startNanos;
    }

    /** How {@link #finish} ended: {@link #COMMITTED}, {@link #ROLLED_BACK} or {@link #COMMIT_FAILED}. */
    public String getResult() {
        return result;
    }

    /** Duration of the commit in nanoseconds, or -1 when none was attempted. */
    public long getCommitNanos() {
        return commitNanos;
    }

    protected void commit() throws StorageException {
    }

    protected void rollback() {
    }

}
