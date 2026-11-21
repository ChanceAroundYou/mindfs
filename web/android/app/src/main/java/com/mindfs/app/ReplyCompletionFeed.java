package com.mindfs.app;

import java.util.ArrayList;
import java.util.List;

/**
 * Client-side consumer for the server's completion ledger (`completedSeq` / `completedEpoch` /
 * `completed` in the replying-sessions payload).
 *
 * <p>The old DONE detection was an edge: "this session was replying in the previous frame and is
 * gone now". That only works while the phone is actually polling. If the process is frozen or
 * killed for a while — which is exactly what backgrounded WebView shells suffer from — the edge
 * never happens and the completion is lost forever. The server now keeps a short ledger of
 * finished turns, and this class turns "what the client has already consumed" plus "what the
 * server still has" into the alerts that must fire now.</p>
 *
 * <p>Contract (mirrors {@code stream_hub_completions.go}):</p>
 * <ul>
 *   <li>No cursor yet ⇒ seed to the head and stay silent (a fresh install must not be flooded
 *       with yesterday's completions).</li>
 *   <li>Epoch changed ⇒ the server restarted and its ledger is gone; reseed silently.</li>
 *   <li>Head seq went backwards ⇒ same thing, reseed silently.</li>
 *   <li>Otherwise alert every new row except sub-sessions (child agents must not buzz the phone),
 *       capped at {@link #MAX_ALERTS} newest so a day offline does not post fifty notifications.</li>
 * </ul>
 *
 * <p>Delivery is at-least-once: the caller persists {@link Decision#next} after applying the
 * alerts, so a crash in between can repeat an alert but never drops one.</p>
 *
 * <p>No Android imports: this class is unit-tested on the JVM.</p>
 */
final class ReplyCompletionFeed {
    /** Newest completions to surface after a long offline stretch; older ones are consumed silently. */
    static final int MAX_ALERTS = 8;
    private static final char CURSOR_SEPARATOR = '\u001f';

    /** Where this client has consumed up to, per server process (epoch) and per monotonic seq. */
    static final class Cursor {
        String epoch = "";
        long seq = 0L;

        boolean seeded() {
            return epoch != null && !epoch.trim().isEmpty();
        }

        /** "epoch␟seq" — the value persisted per node. Empty when never seeded. */
        String encode() {
            return seeded() ? epoch.trim() + CURSOR_SEPARATOR + seq : "";
        }

        static Cursor decode(String raw) {
            String value = raw == null ? "" : raw.trim();
            Cursor cursor = new Cursor();
            int at = value.lastIndexOf(CURSOR_SEPARATOR);
            if (at <= 0) {
                return cursor;
            }
            long seq;
            try {
                seq = Long.parseLong(value.substring(at + 1).trim());
            } catch (NumberFormatException ex) {
                return new Cursor();
            }
            cursor.epoch = value.substring(0, at);
            cursor.seq = Math.max(0L, seq);
            return cursor;
        }

        boolean sameAs(Cursor other) {
            if (other == null) {
                return !seeded();
            }
            return encode().equals(other.encode());
        }
    }

    /** One finished turn from the server's ledger. */
    static final class Completion {
        final long seq;
        final String rootId;
        final String rootTitle;
        final String sessionKey;
        final String sessionTitle;
        final String summary;
        final boolean subSession;
        /** 任务内部步骤（工作流节点回合）：与 subSession 一样静默，只跳过完成提醒。 */
        final boolean silent;

        Completion(long seq, String rootId, String rootTitle, String sessionKey, String sessionTitle,
                   String summary, boolean subSession, boolean silent) {
            this.seq = seq;
            this.rootId = safe(rootId);
            this.rootTitle = safe(rootTitle);
            this.sessionKey = safe(sessionKey);
            this.sessionTitle = safe(sessionTitle);
            this.summary = safe(summary);
            this.subSession = subSession;
            this.silent = silent;
        }
    }

    /** What to alert now and where the cursor must be persisted afterwards. */
    static final class Decision {
        final List<Completion> alerts = new ArrayList<>();
        final Cursor next = new Cursor();
        /** True when the cursor was (re)seeded: the caller must persist it, nothing to alert. */
        boolean seeded;
        /** True when a previous ledger was abandoned because the server restarted. */
        boolean reset;
    }

    /**
     * @param current   what the client consumed so far (never null; an unseeded cursor means none)
     * @param headEpoch server process identity reported in this response; empty means the server
     *                  has no ledger (old build) and the caller should keep using edge detection
     * @param headSeq   highest seq the server knows about
     * @param backlog   rows the server returned for this request, ascending by seq (may be null)
     */
    static Decision decide(Cursor current, String headEpoch, long headSeq, List<Completion> backlog) {
        Decision decision = new Decision();
        Cursor base = current == null ? new Cursor() : current;
        String epoch = safe(headEpoch);
        long head = Math.max(0L, headSeq);
        if (epoch.isEmpty()) {
            decision.next.epoch = base.epoch;
            decision.next.seq = base.seq;
            return decision;
        }
        boolean resync = !base.seeded() || !epoch.equals(base.epoch) || head < base.seq;
        if (resync) {
            decision.reset = base.seeded() && !epoch.equals(base.epoch);
            decision.seeded = true;
            decision.next.epoch = epoch;
            decision.next.seq = head;
            return decision;
        }
        List<Completion> fresh = new ArrayList<>();
        if (backlog != null) {
            for (Completion completion : backlog) {
                if (completion == null || completion.seq <= base.seq || completion.seq > head) {
                    continue;
                }
                if (completion.subSession || completion.silent) {
                    // 子任务 / 任务内部步骤跑完不提醒：用户要的是「活干完了」。
                    continue;
                }
                fresh.add(completion);
            }
        }
        int from = Math.max(0, fresh.size() - MAX_ALERTS);
        decision.alerts.addAll(fresh.subList(from, fresh.size()));
        decision.next.epoch = epoch;
        decision.next.seq = head;
        return decision;
    }

    private static String safe(String value) {
        return value == null ? "" : value.trim();
    }

    private ReplyCompletionFeed() {
    }
}
