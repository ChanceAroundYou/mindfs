package com.mindfs.app;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

import java.util.ArrayList;
import java.util.List;
import org.junit.Test;

public class ReplyCompletionFeedTest {

    private static ReplyCompletionFeed.Cursor cursor(String epoch, long seq) {
        ReplyCompletionFeed.Cursor cursor = new ReplyCompletionFeed.Cursor();
        cursor.epoch = epoch;
        cursor.seq = seq;
        return cursor;
    }

    private static ReplyCompletionFeed.Completion done(long seq, String sessionKey) {
        return new ReplyCompletionFeed.Completion(seq, "root-1", "Proj", sessionKey, sessionKey, "summary", false);
    }

    private static ReplyCompletionFeed.Completion sub(long seq, String sessionKey) {
        return new ReplyCompletionFeed.Completion(seq, "root-1", "Proj", sessionKey, sessionKey, "summary", true);
    }

    private static List<ReplyCompletionFeed.Completion> list(ReplyCompletionFeed.Completion... items) {
        List<ReplyCompletionFeed.Completion> out = new ArrayList<>();
        for (ReplyCompletionFeed.Completion item : items) {
            out.add(item);
        }
        return out;
    }

    @Test
    public void seedsWithoutAlerting() {
        ReplyCompletionFeed.Decision decision = ReplyCompletionFeed.decide(
            new ReplyCompletionFeed.Cursor(), "ep-1", 5L, list(done(4, "s-a"), done(5, "s-b")));
        assertTrue(decision.seeded);
        assertFalse(decision.reset);
        assertEquals(0, decision.alerts.size());
        assertEquals("ep-1", decision.next.epoch);
        assertEquals(5L, decision.next.seq);
    }

    @Test
    public void alertsOnlyRowsAheadOfCursor() {
        ReplyCompletionFeed.Decision decision = ReplyCompletionFeed.decide(
            cursor("ep-1", 2L), "ep-1", 4L, list(done(1, "s-a"), done(2, "s-b"), done(3, "s-c"), done(4, "s-d")));
        assertFalse(decision.seeded);
        assertEquals(2, decision.alerts.size());
        assertEquals("s-c", decision.alerts.get(0).sessionKey);
        assertEquals("s-d", decision.alerts.get(1).sessionKey);
        assertEquals(4L, decision.next.seq);
    }

    @Test
    public void subSessionsAdvanceCursorWithoutAlerting() {
        ReplyCompletionFeed.Decision decision = ReplyCompletionFeed.decide(
            cursor("ep-1", 2L), "ep-1", 4L, list(done(3, "s-main"), sub(4, "s-child")));
        assertEquals(1, decision.alerts.size());
        assertEquals("s-main", decision.alerts.get(0).sessionKey);
        assertEquals(4L, decision.next.seq);
    }

    @Test
    public void reseedsSilentlyWhenServerRestarted() {
        ReplyCompletionFeed.Decision decision = ReplyCompletionFeed.decide(
            cursor("ep-old", 40L), "ep-new", 2L, list(done(1, "s-a"), done(2, "s-b")));
        assertTrue(decision.reset);
        assertTrue(decision.seeded);
        assertEquals(0, decision.alerts.size());
        assertEquals("ep-new", decision.next.epoch);
        assertEquals(2L, decision.next.seq);
    }

    @Test
    public void reseedsSilentlyWhenHeadGoesBackwards() {
        ReplyCompletionFeed.Decision decision = ReplyCompletionFeed.decide(
            cursor("ep-1", 40L), "ep-1", 3L, list(done(4, "s-a")));
        assertEquals(0, decision.alerts.size());
        assertEquals(3L, decision.next.seq);
    }

    @Test
    public void capsAlertsToTheNewestOnes() {
        List<ReplyCompletionFeed.Completion> backlog = new ArrayList<>();
        for (int i = 1; i <= 20; i++) {
            backlog.add(done(i, "s-" + i));
        }
        ReplyCompletionFeed.Decision decision = ReplyCompletionFeed.decide(
            cursor("ep-1", 0L), "ep-1", 20L, backlog);
        assertEquals(ReplyCompletionFeed.MAX_ALERTS, decision.alerts.size());
        // 离线一整天醒来只弹最新几条，但游标仍推到 head —— 老的不再重复弹。
        assertEquals(20L - ReplyCompletionFeed.MAX_ALERTS + 1, decision.alerts.get(0).seq);
        assertEquals(20L, decision.alerts.get(decision.alerts.size() - 1).seq);
        assertEquals(20L, decision.next.seq);
    }

    @Test
    public void keepsCursorWhenServerHasNoLedger() {
        ReplyCompletionFeed.Cursor base = cursor("ep-1", 7L);
        ReplyCompletionFeed.Decision decision = ReplyCompletionFeed.decide(base, "", 0L, null);
        assertFalse(decision.seeded);
        assertFalse(decision.reset);
        assertEquals(0, decision.alerts.size());
        assertTrue(decision.next.sameAs(base));
    }

    @Test
    public void cursorRoundTripsThroughPreferencesString() {
        ReplyCompletionFeed.Cursor base = cursor("ep-9", 123L);
        String encoded = base.encode();
        ReplyCompletionFeed.Cursor decoded = ReplyCompletionFeed.Cursor.decode(encoded);
        assertEquals("ep-9", decoded.epoch);
        assertEquals(123L, decoded.seq);
        assertTrue(base.sameAs(decoded));
        assertFalse(new ReplyCompletionFeed.Cursor().seeded());
        assertFalse(ReplyCompletionFeed.Cursor.decode("").seeded());
        assertFalse(ReplyCompletionFeed.Cursor.decode("garbage").seeded());
    }
}
