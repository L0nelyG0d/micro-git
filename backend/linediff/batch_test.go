package linediff_test

import (
	"testing"

	"github.com/L0nelyG0d/micro-git/backend/gitobj"
	"github.com/L0nelyG0d/micro-git/backend/linediff"
	"github.com/L0nelyG0d/micro-git/backend/treebuild"
)

// batchStore counts GetObject and GetObjects calls over a MemStore.
type batchStore struct {
	*treebuild.MemStore
	single, batches int
}

func (b *batchStore) GetObject(oid string) (gitobj.Type, []byte, error) {
	b.single++
	return b.MemStore.GetObject(oid)
}

func (b *batchStore) GetObjects(oids []string) (map[string]linediff.Object, error) {
	b.batches++
	out := map[string]linediff.Object{}
	for _, oid := range oids {
		if t, body, err := b.MemStore.GetObject(oid); err == nil {
			out[oid] = linediff.Object{Type: t, Body: body}
		}
	}
	return out, nil
}

func TestTreesBatchesPerLevel(t *testing.T) {
	ms := treebuild.NewMemStore()
	build := func(content string) string {
		root, err := treebuild.Apply(ms, "", []treebuild.Change{
			treebuild.Upsert("a/b/x", content), treebuild.Upsert("a/y", content), treebuild.Upsert("c/z", content)})
		if err != nil {
			t.Fatal(err)
		}
		return root
	}
	oldT, newT := build("1\n"), build("2\n")
	bs := &batchStore{MemStore: ms}
	got, err := linediff.Trees(bs, oldT, newT)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("files = %d, want 3", len(got))
	}
	// levels: root, {a,c}, {a/b}, then one batch for all six blobs
	if bs.batches != 4 || bs.single != 0 {
		t.Errorf("batches=%d single=%d, want 4 and 0", bs.batches, bs.single)
	}
}
