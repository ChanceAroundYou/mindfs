package controlsync

import (
	"strconv"
	"testing"
)

func seedChanges(t *testing.T, store *Store, n int) int64 {
	t.Helper()
	ctx := t.Context()
	for i := 0; i < n; i++ {
		r := rec(DomainPreferences, "k"+strconv.Itoa(i), `{"v":`+strconv.Itoa(i)+`}`)
		r.Hash = hashOf(t, r)
		if _, err := store.WriteRecords(ctx, []Record{r}); err != nil {
			t.Fatalf("WriteRecords: %v", err)
		}
	}
	head, err := store.HeadRev(ctx)
	if err != nil {
		t.Fatalf("HeadRev: %v", err)
	}
	return head
}

func TestChangeFeedHeadAndPageLimits(t *testing.T) {
	store := openTestStore(t)
	feed, err := NewChangeFeed(store)
	if err != nil {
		t.Fatalf("NewChangeFeed: %v", err)
	}
	head := seedChanges(t, store, 5)

	got, err := feed.Head(t.Context())
	if err != nil || got.HeadRev != head {
		t.Fatalf("Head = %+v, %v，head_rev 应为 %d", got, err, head)
	}

	// 不传 limit 用默认值，传超上限夹到硬上限。
	page, pageHead, err := feed.Page(t.Context(), 0, 0)
	if err != nil || len(page) != 5 {
		t.Fatalf("默认 limit 得到 %d 条, %v", len(page), err)
	}
	if pageHead != head {
		t.Fatalf("head_rev = %d，应为 %d", pageHead, head)
	}
	page, _, err = feed.Page(t.Context(), 0, ChangePageMax+100)
	if err != nil || len(page) != 5 {
		t.Fatalf("夹住上限后得到 %d 条, %v", len(page), err)
	}

	// 负数 since 当 0 处理（从头拉），不是报错也不是空页。
	page, _, err = feed.Page(t.Context(), -7, 2)
	if err != nil || len(page) != 2 || page[0].Rev != 1 {
		t.Fatalf("since=-7 limit=2 得到 %+v, %v", page, err)
	}
}
