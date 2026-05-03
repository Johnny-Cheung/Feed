package interaction

import (
	"testing"
	"time"

	"feed-backend/internal/infra/feedcache"
)

func TestSliceCachedCommentBriefsFirstPage(t *testing.T) {
	comments := testCachedComments(30)

	got, ok := sliceCachedCommentBriefs(comments, nil, 20, 50)
	if !ok {
		t.Fatal("expected cache page to be usable")
	}
	if len(got) != 21 {
		t.Fatalf("expected 21 comments, got %d", len(got))
	}
	if got[0].ID != 30 || got[20].ID != 10 {
		t.Fatalf("unexpected page boundaries: first=%d last=%d", got[0].ID, got[20].ID)
	}
}

func TestSliceCachedCommentBriefsCursorInsideCache(t *testing.T) {
	comments := testCachedComments(50)
	cursor := &commentCursor{
		Time: comments[19].CreatedAt,
		ID:   comments[19].ID,
	}

	got, ok := sliceCachedCommentBriefs(comments, cursor, 20, 50)
	if !ok {
		t.Fatal("expected cache page to be usable")
	}
	if len(got) != 21 {
		t.Fatalf("expected 21 comments, got %d", len(got))
	}
	if got[0].ID != comments[20].ID || got[20].ID != comments[40].ID {
		t.Fatalf("unexpected page boundaries: first=%d last=%d", got[0].ID, got[20].ID)
	}
}

func TestSliceCachedCommentBriefsFallsBackWhenPageCrossesFullCache(t *testing.T) {
	comments := testCachedComments(50)
	cursor := &commentCursor{
		Time: comments[44].CreatedAt,
		ID:   comments[44].ID,
	}

	got, ok := sliceCachedCommentBriefs(comments, cursor, 20, 50)
	if ok {
		t.Fatalf("expected cache miss, got %d comments", len(got))
	}
}

func TestSliceCachedCommentBriefsCompleteCacheCanReturnEmptyPage(t *testing.T) {
	comments := testCachedComments(12)
	cursor := &commentCursor{
		Time: comments[len(comments)-1].CreatedAt,
		ID:   comments[len(comments)-1].ID,
	}

	got, ok := sliceCachedCommentBriefs(comments, cursor, 20, 50)
	if !ok {
		t.Fatal("expected complete short cache to be usable")
	}
	if len(got) != 0 {
		t.Fatalf("expected empty page, got %d comments", len(got))
	}
}

func testCachedComments(count int) []feedcache.CommentBrief {
	base := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	comments := make([]feedcache.CommentBrief, 0, count)
	for i := count; i >= 1; i-- {
		comments = append(comments, feedcache.CommentBrief{
			ID:        uint64(i),
			VideoID:   1,
			UserID:    1,
			Content:   "content",
			CreatedAt: base.Add(time.Duration(i) * time.Second),
		})
	}
	return comments
}
