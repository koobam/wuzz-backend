package domain

import "testing"

func TestParseIDs(t *testing.T) {
	ids, err := ParseIDs([]string{"6F9619FF-8B86-D011-B42D-00C04FC964FF", " 6f9619ff-8b86-d011-b42d-00c04fc964ff "})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "6f9619ff-8b86-d011-b42d-00c04fc964ff" {
		t.Fatalf("repeats must collapse to one lowercase id, got %v", ids)
	}
	if _, err := ParseIDs([]string{"not-an-id"}); err == nil {
		t.Fatal("a malformed id must be refused")
	}
	if _, err := ParseIDs(make([]string, MaxBatch+1)); err == nil {
		t.Fatal("more than MaxBatch ids must be refused")
	}
}

func TestListQueryNormalize(t *testing.T) {
	q := ListQuery{Limit: 10_000, Offset: -3}
	if err := q.Normalize(); err != nil {
		t.Fatal(err)
	}
	if q.Limit != MaxPage || q.Offset != 0 {
		t.Fatalf("got limit %d offset %d", q.Limit, q.Offset)
	}
	q = ListQuery{}
	_ = q.Normalize()
	if q.Limit != DefaultPage {
		t.Fatalf("an empty limit takes the default, got %d", q.Limit)
	}
	q = ListQuery{Folder: "secrets"}
	if err := q.Normalize(); err == nil {
		t.Fatal("an unknown folder must be refused")
	}
}
