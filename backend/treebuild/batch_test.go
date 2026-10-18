package treebuild

import (
	"testing"

	"github.com/L0nelyG0d/micro-git/backend/gitobj"
)

// plainStore hides the BatchStore methods of a MemStore.
type plainStore struct{ m *MemStore }

func (p plainStore) GetObject(oid string) (gitobj.Type, []byte, error) { return p.m.GetObject(oid) }
func (p plainStore) PutObject(t gitobj.Type, b []byte) (string, error) { return p.m.PutObject(t, b) }

func TestBatchAndPlainStoresAgree(t *testing.T) {
	base := NewMemStore()
	root, err := Apply(base, "", []Change{Upsert("a/x.txt", "1"), Upsert("a/b/y.txt", "2"), Upsert("c.txt", "3")})
	if err != nil {
		t.Fatal(err)
	}
	changes := []Change{Upsert("a/b/z.txt", "4"), Upsert("a/x.txt", "5"), Remove("c.txt"), Upsert("d/e.txt", "6")}

	batch, plain := NewMemStore(), NewMemStore()
	for _, m := range []*MemStore{batch, plain} {
		_, objs, _ := Build(base, "", []Change{Upsert("a/x.txt", "1"), Upsert("a/b/y.txt", "2"), Upsert("c.txt", "3")})
		if err := m.PutObjects(objs); err != nil {
			t.Fatal(err)
		}
	}
	var _ BatchStore = batch
	got, err := Apply(batch, root, changes)
	if err != nil {
		t.Fatal(err)
	}
	want, err := Apply(plainStore{plain}, root, changes)
	if err != nil {
		t.Fatal(err)
	}
	if got != want || batch.Len() != plain.Len() {
		t.Errorf("batch %s (%d objs), plain %s (%d objs)", got, batch.Len(), want, plain.Len())
	}
}
