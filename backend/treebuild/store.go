package treebuild

import (
	"errors"
	"fmt"
	"sync"

	"github.com/L0nelyG0d/micro-git/backend/gitobj"
)

// ErrNotFound is returned by Store.GetObject when the oid is unknown.
// Implementations should return it (or an error wrapping it).
var ErrNotFound = errors.New("object not found")

// Store is the minimal object store shared by the tree builder (#7) and the
// later packages that read or persist objects (#8, #9, #10). It is scoped to
// one repository: objects are immutable and de-duplicated by oid.
//
// Implementations must be safe for concurrent use and must not retain or
// modify the slices passed to them or returned from them.
type Store interface {
	// GetObject returns the type and raw body of the object with the given
	// oid, or an error wrapping ErrNotFound.
	GetObject(oid string) (gitobj.Type, []byte, error)

	// PutObject stores an object and returns its oid, computed with
	// gitobj.Hash. Putting an object that already exists is not an error.
	PutObject(t gitobj.Type, body []byte) (string, error)
}

// MemStore is an in-memory Store, for tests and for composing in-memory
// repositories.
type MemStore struct {
	mu   sync.Mutex
	objs map[string]memObj
}

type memObj struct {
	t    gitobj.Type
	body []byte
}

// NewMemStore returns an empty MemStore.
func NewMemStore() *MemStore { return &MemStore{objs: map[string]memObj{}} }

// GetObject implements Store.
func (m *MemStore) GetObject(oid string) (gitobj.Type, []byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.objs[oid]
	if !ok {
		return "", nil, fmt.Errorf("%w: %s", ErrNotFound, oid)
	}
	return o.t, append([]byte(nil), o.body...), nil
}

// PutObject implements Store.
func (m *MemStore) PutObject(t gitobj.Type, body []byte) (string, error) {
	oid := gitobj.Hash(t, body)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.objs[oid]; !ok {
		m.objs[oid] = memObj{t: t, body: append([]byte(nil), body...)}
	}
	return oid, nil
}

// Len returns the number of stored objects.
func (m *MemStore) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.objs)
}

// Has reports whether the oid is stored.
func (m *MemStore) Has(oid string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.objs[oid]
	return ok
}
