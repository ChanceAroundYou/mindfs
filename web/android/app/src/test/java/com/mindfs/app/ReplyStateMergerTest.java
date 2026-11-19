package com.mindfs.app;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

import java.util.Arrays;
import java.util.List;
import java.util.Map;

import org.junit.Test;

/**
 * JVM unit tests for the pure merge/DONE logic extracted from the background reply poller.
 *
 * <p>Every case drives the class through its public surface only: applyNode / markNodeFailed /
 * states. The clock is always injected so the tests never depend on wall time.</p>
 */
public class ReplyStateMergerTest {
    private static final long FRESHNESS_MILLIS = 90_000L;

    private static ReplyStateMerger.Entry entry(String nodeId, String rootId, String sessionKey) {
        ReplyStateMerger.Entry entry = new ReplyStateMerger.Entry(nodeId, rootId, sessionKey);
        entry.rootTitle = "root-" + rootId;
        entry.sessionTitle = "session-" + sessionKey;
        entry.summary = "summary-" + sessionKey;
        entry.updatedAt = "2026-11-18T00:00:00Z";
        return entry;
    }

    private static List<ReplyStateMerger.Entry> entries(ReplyStateMerger.Entry... items) {
        return Arrays.asList(items);
    }

    @Test
    public void newReplyingEntryIsLiveAndNotCompleted() {
        ReplyStateMerger merger = new ReplyStateMerger(FRESHNESS_MILLIS);
        ReplyStateMerger.Entry replying = entry("home", "root-1", "s-1");

        ReplyStateMerger.Result result = merger.applyNode("home", entries(replying), 1_000L);

        assertTrue(result.newlyCompleted.isEmpty());
        assertEquals(1, result.live.size());
        assertEquals("s-1", result.live.get(0).sessionKey);
        assertTrue(result.currentKeys.contains(replying.key()));
        assertEquals(1, merger.states().size());
    }

    @Test
    public void missingEntryIsCompletedWithLastKnownFieldsThenPruned() {
        ReplyStateMerger merger = new ReplyStateMerger(FRESHNESS_MILLIS);
        ReplyStateMerger.Entry replying = entry("home", "root-1", "s-1");
        replying.summary = "last known summary";
        merger.applyNode("home", entries(replying), 1_000L);

        ReplyStateMerger.Result result = merger.applyNode("home", entries(), 2_000L);

        assertEquals(1, result.newlyCompleted.size());
        ReplyStateMerger.Entry done = result.newlyCompleted.get(0);
        assertEquals("s-1", done.sessionKey);
        assertEquals("last known summary", done.summary);
        assertEquals("session-s-1", done.sessionTitle);
        assertEquals("root-root-1", done.rootTitle);
        assertEquals("2026-11-18T00:00:00Z", done.updatedAt);
        assertTrue(result.live.isEmpty());
        assertFalse(result.currentKeys.contains(replying.key()));
        assertFalse(merger.states().containsKey(replying.key()));
    }

    @Test
    public void markNodeFailedKeepsEntriesButRemovesThemFromLive() {
        ReplyStateMerger merger = new ReplyStateMerger(FRESHNESS_MILLIS);
        ReplyStateMerger.Entry homeEntry = entry("home", "root-1", "s-1");
        ReplyStateMerger.Entry fnEntry = entry("fn", "root-2", "s-2");
        merger.applyNode("home", entries(homeEntry), 1_000L);

        merger.markNodeFailed("home");
        ReplyStateMerger.Result result = merger.applyNode("fn", entries(fnEntry), 2_000L);

        assertEquals(1, result.live.size());
        assertEquals("fn", result.live.get(0).nodeId);
        assertFalse(result.currentKeys.contains(homeEntry.key()));
        assertTrue(merger.states().containsKey(homeEntry.key()));
    }

    @Test
    public void staleNodeEntriesAreKeptButRemovedFromLive() {
        ReplyStateMerger merger = new ReplyStateMerger(FRESHNESS_MILLIS);
        ReplyStateMerger.Entry homeEntry = entry("home", "root-1", "s-1");
        ReplyStateMerger.Entry fnEntry = entry("fn", "root-2", "s-2");
        merger.applyNode("home", entries(homeEntry), 1_000L);

        ReplyStateMerger.Result result = merger.applyNode(
            "fn", entries(fnEntry), 1_000L + FRESHNESS_MILLIS + 1L);

        assertEquals(1, result.live.size());
        assertEquals("fn", result.live.get(0).nodeId);
        assertFalse(result.currentKeys.contains(homeEntry.key()));
        assertTrue(merger.states().containsKey(homeEntry.key()));
    }

    @Test
    public void awaitingEntryIsLiveEvenWhenNotReplying() {
        ReplyStateMerger merger = new ReplyStateMerger(FRESHNESS_MILLIS);
        ReplyStateMerger.Entry waiting = entry("home", "root-1", "s-1");
        waiting.replying = false;
        waiting.awaiting = true;

        ReplyStateMerger.Result result = merger.applyNode("home", entries(waiting), 1_000L);

        assertTrue(result.newlyCompleted.isEmpty());
        assertEquals(1, result.live.size());
        assertEquals("s-1", result.live.get(0).sessionKey);
    }

    @Test
    public void completedKeyReappearingInFreshSnapshotIsLiveAgain() {
        ReplyStateMerger merger = new ReplyStateMerger(FRESHNESS_MILLIS);
        merger.applyNode("home", entries(entry("home", "root-1", "s-1")), 1_000L);
        merger.applyNode("home", entries(), 2_000L);

        ReplyStateMerger.Entry again = entry("home", "root-1", "s-1");
        ReplyStateMerger.Result reappeared = merger.applyNode("home", entries(again), 3_000L);

        assertTrue(reappeared.newlyCompleted.isEmpty());
        assertEquals(1, reappeared.live.size());
        assertTrue(merger.states().containsKey(again.key()));

        ReplyStateMerger.Result doneAgain = merger.applyNode("home", entries(), 4_000L);

        assertEquals(1, doneAgain.newlyCompleted.size());
        assertEquals("s-1", doneAgain.newlyCompleted.get(0).sessionKey);
    }

    @Test
    public void sameSessionKeyOnAnotherNodeIsIndependent() {
        ReplyStateMerger merger = new ReplyStateMerger(FRESHNESS_MILLIS);
        ReplyStateMerger.Entry homeEntry = entry("home", "root-1", "s-1");
        ReplyStateMerger.Entry fnEntry = entry("fn", "root-1", "s-1");
        merger.applyNode("home", entries(homeEntry), 1_000L);

        ReplyStateMerger.Result both = merger.applyNode("fn", entries(fnEntry), 1_000L);

        assertEquals(2, both.live.size());
        assertEquals(2, both.currentKeys.size());
        assertEquals(2, merger.states().size());

        ReplyStateMerger.Result homeDone = merger.applyNode("home", entries(), 2_000L);

        assertEquals(1, homeDone.newlyCompleted.size());
        assertEquals("home", homeDone.newlyCompleted.get(0).nodeId);
        assertEquals(1, homeDone.live.size());
        assertEquals("fn", homeDone.live.get(0).nodeId);
        assertTrue(merger.states().containsKey(fnEntry.key()));
    }

    @Test
    public void nullEntriesMeanNodeHasNoReplyingSessions() {
        ReplyStateMerger merger = new ReplyStateMerger(FRESHNESS_MILLIS);

        ReplyStateMerger.Result first = merger.applyNode("home", null, 1_000L);

        assertTrue(first.live.isEmpty());
        assertTrue(first.newlyCompleted.isEmpty());
        assertTrue(first.currentKeys.isEmpty());
        assertTrue(merger.states().isEmpty());

        merger.applyNode("home", entries(entry("home", "root-1", "s-1")), 2_000L);
        ReplyStateMerger.Result result = merger.applyNode("home", null, 3_000L);

        assertEquals(1, result.newlyCompleted.size());
        assertTrue(merger.states().isEmpty());
    }

    @Test
    public void statesReturnsSnapshotCopy() {
        ReplyStateMerger merger = new ReplyStateMerger(FRESHNESS_MILLIS);
        merger.applyNode("home", entries(entry("home", "root-1", "s-1")), 1_000L);

        Map<String, ReplyStateMerger.Entry> snapshot = merger.states();
        merger.applyNode(
            "home",
            entries(entry("home", "root-1", "s-1"), entry("home", "root-1", "s-2")),
            2_000L);

        assertEquals(1, snapshot.size());
        assertEquals(2, merger.states().size());
    }

    /**
     * 子会话标记必须活到 DONE 判定那一刻：完成是靠「从快照里消失」判出来的，
     * 服务端 push 下来的 subSession 只要在中途丢了，子任务回来就会照常弹提醒。
     */
    @Test
    public void subSessionFlagSurvivesIntoCompletedEntry() {
        ReplyStateMerger merger = new ReplyStateMerger(FRESHNESS_MILLIS);
        ReplyStateMerger.Entry child = entry("home", "root-1", "child-1");
        child.subSession = true;
        merger.applyNode("home", entries(child), 1_000L);

        ReplyStateMerger.Result result = merger.applyNode("home", entries(), 2_000L);

        assertEquals(1, result.newlyCompleted.size());
        assertTrue(result.newlyCompleted.get(0).subSession);
    }
}