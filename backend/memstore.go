package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/L0nelyG0d/micro-git/backend/gitobj"
	"github.com/L0nelyG0d/micro-git/backend/linediff"
	"github.com/L0nelyG0d/micro-git/backend/treebuild"
)

// memStore is the in-memory implementation of Store (STORE=memory): maps behind
// one RWMutex. Nothing is persisted; the data is gone when the process exits.
// It mirrors the pgx store's observable behaviour, including the sentinel
// errors, case-insensitive uniqueness, compare-and-swap refs, the foreign keys
// (a ref needs its object, objects need their repository) and cascade delete.
type memStore struct {
	d    *memData
	undo *[]func() // non-nil inside InTx: how to revert each write, newest last
}

var _ Store = (*memStore)(nil)

// memData is the shared state of one memory database.
type memData struct {
	mu         sync.RWMutex
	txMu       sync.Mutex // serializes InTx calls
	nextUserID int64
	nextRepoID int64
	users      map[int64]*memUser
	userByName map[string]int64 // lower-cased username -> id
	repos      map[int64]*memRepo
}

type memUser struct {
	id           int64
	username     string
	passwordHash string
}

type memRepo struct {
	id            int64
	ownerID       int64
	name          string
	description   string
	defaultBranch string
	private       bool
	createdAt     time.Time
	objects       map[string]memObject
	refs          map[string]string
}

type memObject struct {
	typ  gitobj.Type
	body []byte
}

// newMemStore returns an empty memory store.
func newMemStore() *memStore {
	return &memStore{d: &memData{
		users:      map[int64]*memUser{},
		userByName: map[string]int64{},
		repos:      map[int64]*memRepo{},
	}}
}

// record registers how to revert a write; callers hold d.mu for writing.
func (m *memStore) record(f func()) {
	if m.undo != nil {
		*m.undo = append(*m.undo, f)
	}
}

func cloneBytes(b []byte) []byte { return append([]byte{}, b...) }

// InTx runs fn; when it returns an error (or panics) every write made through
// the Store it was given is reverted. Calls are serialized. Writes become
// visible to other readers as they happen (there is no isolation), which is
// enough for the commit path this store serves.
func (m *memStore) InTx(ctx context.Context, fn func(Store) error) (err error) {
	if m.undo != nil { // already in a transaction: join it
		return fn(m)
	}
	m.d.txMu.Lock()
	defer m.d.txMu.Unlock()
	var log []func()
	tx := &memStore{d: m.d, undo: &log}
	done := false
	defer func() {
		if done && err == nil {
			return
		}
		m.d.mu.Lock()
		defer m.d.mu.Unlock()
		for i := len(log) - 1; i >= 0; i-- {
			log[i]()
		}
	}()
	err = fn(tx)
	done = true
	return err
}

// Users.

func (m *memStore) CreateUser(ctx context.Context, username, passwordHash string) (int64, error) {
	m.d.mu.Lock()
	defer m.d.mu.Unlock()
	key := strings.ToLower(username)
	if _, ok := m.d.userByName[key]; ok {
		return 0, ErrConflict
	}
	m.d.nextUserID++
	id := m.d.nextUserID
	m.d.users[id] = &memUser{id: id, username: username, passwordHash: passwordHash}
	m.d.userByName[key] = id
	m.record(func() {
		delete(m.d.users, id)
		delete(m.d.userByName, key)
	})
	return id, nil
}

func (m *memStore) UsernameByID(ctx context.Context, id int64) (string, error) {
	m.d.mu.RLock()
	defer m.d.mu.RUnlock()
	u, ok := m.d.users[id]
	if !ok {
		return "", ErrNotFound
	}
	return u.username, nil
}

func (m *memStore) UserForLogin(ctx context.Context, username string) (int64, string, error) {
	m.d.mu.RLock()
	defer m.d.mu.RUnlock()
	id, ok := m.d.userByName[strings.ToLower(username)]
	if !ok {
		return 0, "", ErrNotFound
	}
	return id, m.d.users[id].passwordHash, nil
}

// Repos.

// repoView builds the API form of a repository; callers hold d.mu.
func (d *memData) repoView(r *memRepo) Repo {
	return Repo{
		ID:            r.id,
		Owner:         d.users[r.ownerID].username,
		Name:          r.name,
		Description:   r.description,
		DefaultBranch: r.defaultBranch,
		Private:       r.private,
		Empty:         len(r.refs) == 0,
		CreatedAt:     r.createdAt.UTC().Format(time.RFC3339),
		OwnerID:       r.ownerID,
	}
}

// findRepo looks a repository up by owner and name, case-insensitively.
func (d *memData) findRepo(owner, name string) *memRepo {
	uid, ok := d.userByName[strings.ToLower(owner)]
	if !ok {
		return nil
	}
	for _, r := range d.repos {
		if r.ownerID == uid && strings.EqualFold(r.name, name) {
			return r
		}
	}
	return nil
}

// nameTaken reports whether the owner has a repository called name other than skip.
func (d *memData) nameTaken(ownerID int64, name string, skip int64) bool {
	for _, r := range d.repos {
		if r.ownerID == ownerID && r.id != skip && strings.EqualFold(r.name, name) {
			return true
		}
	}
	return false
}

func (m *memStore) FindRepo(ctx context.Context, owner, name string) (Repo, error) {
	m.d.mu.RLock()
	defer m.d.mu.RUnlock()
	r := m.d.findRepo(owner, name)
	if r == nil {
		return Repo{}, ErrNotFound
	}
	return m.d.repoView(r), nil
}

var (
	treeHeader   = regexp.MustCompile(treeHeaderRE)
	parentHeader = regexp.MustCompile(parentHeaderRE)
	parentLine   = regexp.MustCompile(`^parent ([0-9a-f]{40})$`)
)

// head returns the first 100 bytes, which cover "tree <40>\nparent <40>\n".
func head(b []byte) []byte {
	if len(b) > 100 {
		return b[:100]
	}
	return b
}

func (r *memRepo) commit(oid string) (memObject, bool) {
	o, ok := r.objects[oid]
	return o, ok && o.typ == gitobj.TypeCommit
}

func (m *memStore) FindRepoRef(ctx context.Context, owner, name, ref string) (ReadHead, error) {
	m.d.mu.RLock()
	defer m.d.mu.RUnlock()
	r := m.d.findRepo(owner, name)
	if r == nil {
		return ReadHead{}, ErrNotFound
	}
	h := ReadHead{Repo: m.d.repoView(r)}
	tip, ok := r.refs[ref]
	if !ok {
		return h, nil
	}
	h.HasRef, h.Tip = true, tip
	h.Objects = map[string]linediff.Object{}
	if c, ok := r.commit(tip); ok {
		h.Objects[tip] = linediff.Object{Type: gitobj.TypeCommit, Body: cloneBytes(c.body)}
		if mt := treeHeader.FindSubmatch(head(c.body)); mt != nil {
			if t, ok := r.objects[string(mt[1])]; ok && t.typ == gitobj.TypeTree {
				h.Objects[string(mt[1])] = linediff.Object{Type: gitobj.TypeTree, Body: cloneBytes(t.body)}
			}
		}
	}
	return h, nil
}

func (m *memStore) CreateRepo(ctx context.Context, ownerID int64, name, description string, private bool, createdAt time.Time) (Repo, error) {
	m.d.mu.Lock()
	defer m.d.mu.Unlock()
	if _, ok := m.d.users[ownerID]; !ok {
		return Repo{}, ErrForeignKey
	}
	if m.d.nameTaken(ownerID, name, 0) {
		return Repo{}, ErrConflict
	}
	m.d.nextRepoID++
	r := &memRepo{
		id: m.d.nextRepoID, ownerID: ownerID, name: name, description: description,
		defaultBranch: "main", private: private, createdAt: createdAt.UTC(),
		objects: map[string]memObject{}, refs: map[string]string{},
	}
	m.d.repos[r.id] = r
	m.record(func() { delete(m.d.repos, r.id) })
	return m.d.repoView(r), nil
}

func (m *memStore) UpdateRepo(ctx context.Context, repoID int64, p RepoPatch) (Repo, error) {
	if p.Name == nil && p.Description == nil && p.DefaultBranch == nil && p.Private == nil {
		return Repo{}, errors.New("UpdateRepo: nothing to update")
	}
	m.d.mu.Lock()
	defer m.d.mu.Unlock()
	r, ok := m.d.repos[repoID]
	if !ok {
		return Repo{}, ErrNotFound
	}
	if p.DefaultBranch != nil {
		if _, ok := r.refs[*p.DefaultBranch]; !ok {
			return Repo{}, ErrNotFound
		}
	}
	if p.Name != nil && m.d.nameTaken(r.ownerID, *p.Name, r.id) {
		return Repo{}, ErrConflict
	}
	old := *r
	if p.Name != nil {
		r.name = *p.Name
	}
	if p.Description != nil {
		r.description = *p.Description
	}
	if p.DefaultBranch != nil {
		r.defaultBranch = *p.DefaultBranch
	}
	if p.Private != nil {
		r.private = *p.Private
	}
	m.record(func() {
		if cur, ok := m.d.repos[old.id]; ok && cur == r {
			r.name, r.description, r.defaultBranch, r.private = old.name, old.description, old.defaultBranch, old.private
		}
	})
	return m.d.repoView(r), nil
}

func (m *memStore) DeleteRepo(ctx context.Context, repoID int64) error {
	m.d.mu.Lock()
	defer m.d.mu.Unlock()
	r, ok := m.d.repos[repoID]
	if !ok {
		return nil
	}
	delete(m.d.repos, repoID) // objects and refs go with it
	m.record(func() {
		if _, taken := m.d.repos[repoID]; !taken {
			m.d.repos[repoID] = r
		}
	})
	return nil
}

func (m *memStore) RepoExists(ctx context.Context, repoID int64) (bool, error) {
	m.d.mu.RLock()
	defer m.d.mu.RUnlock()
	_, ok := m.d.repos[repoID]
	return ok, nil
}

// Visibility.

func (m *memStore) ListRepos(ctx context.Context, viewerID int64, owner *string, limit, offset int) ([]Repo, error) {
	m.d.mu.RLock()
	defer m.d.mu.RUnlock()
	var rs []*memRepo
	for _, r := range m.d.repos {
		if r.private && r.ownerID != viewerID {
			continue
		}
		if owner != nil && !strings.EqualFold(m.d.users[r.ownerID].username, *owner) {
			continue
		}
		rs = append(rs, r)
	}
	sort.Slice(rs, func(i, j int) bool {
		if !rs[i].createdAt.Equal(rs[j].createdAt) {
			return rs[i].createdAt.After(rs[j].createdAt)
		}
		return rs[i].id > rs[j].id
	})
	out := []Repo{}
	for i := offset; i < len(rs) && len(out) < limit; i++ {
		if i < 0 {
			continue
		}
		out = append(out, m.d.repoView(rs[i]))
	}
	return out, nil
}

// Objects.

// memObjects is the treebuild.Store of one repository.
type memObjects struct {
	m      *memStore
	repoID int64
}

var (
	_ treebuild.Store      = (*memObjects)(nil)
	_ treebuild.BatchStore = (*memObjects)(nil)
)

func (m *memStore) Objects(ctx context.Context, repoID int64) treebuild.Store {
	return &memObjects{m: m, repoID: repoID}
}

func (o *memObjects) GetObject(oid string) (gitobj.Type, []byte, error) {
	o.m.d.mu.RLock()
	defer o.m.d.mu.RUnlock()
	if r, ok := o.m.d.repos[o.repoID]; ok {
		if ob, ok := r.objects[oid]; ok {
			return ob.typ, cloneBytes(ob.body), nil
		}
	}
	return "", nil, fmt.Errorf("%w: %s", treebuild.ErrNotFound, oid)
}

func (o *memObjects) PutObject(t gitobj.Type, body []byte) (string, error) {
	oid := gitobj.Hash(t, body)
	o.m.d.mu.Lock()
	defer o.m.d.mu.Unlock()
	if err := o.m.putLocked(o.repoID, []string{oid}, []treebuild.Object{{Type: t, Body: body}}); err != nil {
		return "", err
	}
	return oid, nil
}

func (o *memObjects) PutObjects(objs []treebuild.Object) error {
	if len(objs) == 0 {
		return nil
	}
	oids := make([]string, len(objs))
	for i, ob := range objs {
		oids[i] = gitobj.Hash(ob.Type, ob.Body)
	}
	o.m.d.mu.Lock()
	defer o.m.d.mu.Unlock()
	return o.m.putLocked(o.repoID, oids, objs)
}

func (o *memObjects) GetObjects(oids []string) (map[string]treebuild.Object, error) {
	for _, oid := range oids {
		if err := checkOID(oid); err != nil {
			return nil, err
		}
	}
	o.m.d.mu.RLock()
	defer o.m.d.mu.RUnlock()
	out := make(map[string]treebuild.Object, len(oids))
	r, ok := o.m.d.repos[o.repoID]
	if !ok {
		return out, nil
	}
	for _, oid := range oids {
		if ob, ok := r.objects[oid]; ok {
			out[oid] = treebuild.Object{Type: ob.typ, Body: cloneBytes(ob.body)}
		}
	}
	return out, nil
}

// putLocked inserts objects that are not stored yet (insert-if-absent);
// ErrForeignKey if the repository is gone. The caller holds d.mu for writing.
func (m *memStore) putLocked(repoID int64, oids []string, objs []treebuild.Object) error {
	r, ok := m.d.repos[repoID]
	if !ok {
		return fmt.Errorf("%w: repository %d", ErrForeignKey, repoID)
	}
	for i, ob := range objs {
		oid := oids[i]
		if _, exists := r.objects[oid]; exists {
			continue
		}
		r.objects[oid] = memObject{typ: ob.Type, body: cloneBytes(ob.Body)}
		m.record(func() {
			if cur, ok := m.d.repos[repoID]; ok && cur == r {
				delete(r.objects, oid)
			}
		})
	}
	return nil
}

func (m *memStore) CommitBundle(ctx context.Context, repoID int64, oid string) (map[string]linediff.Object, bool, error) {
	if err := checkOID(oid); err != nil {
		return nil, false, err
	}
	m.d.mu.RLock()
	defer m.d.mu.RUnlock()
	objs := map[string]linediff.Object{}
	r, ok := m.d.repos[repoID]
	if !ok {
		return objs, false, nil
	}
	c, ok := r.commit(oid)
	if !ok {
		return objs, false, nil
	}
	add := func(id string, o memObject) {
		objs[id] = linediff.Object{Type: o.typ, Body: cloneBytes(o.body)}
	}
	addTree := func(body []byte) {
		if mt := treeHeader.FindSubmatch(head(body)); mt != nil {
			if t, ok := r.objects[string(mt[1])]; ok && t.typ == gitobj.TypeTree {
				add(string(mt[1]), t)
			}
		}
	}
	add(oid, c)
	addTree(c.body)
	if mt := parentHeader.FindSubmatch(head(c.body)); mt != nil {
		if p, ok := r.commit(string(mt[1])); ok {
			add(string(mt[1]), p)
			addTree(p.body)
		}
	}
	return objs, true, nil
}

// Refs.

func (m *memStore) GetRef(ctx context.Context, repoID int64, name string) (string, error) {
	m.d.mu.RLock()
	defer m.d.mu.RUnlock()
	if r, ok := m.d.repos[repoID]; ok {
		if oid, ok := r.refs[name]; ok {
			return oid, nil
		}
	}
	return "", ErrNotFound
}

func (m *memStore) GetRefCommit(ctx context.Context, repoID int64, name string) (string, []byte, error) {
	m.d.mu.RLock()
	defer m.d.mu.RUnlock()
	if r, ok := m.d.repos[repoID]; ok {
		if oid, ok := r.refs[name]; ok {
			if o, ok := r.objects[oid]; ok {
				return oid, cloneBytes(o.body), nil
			}
		}
	}
	return "", nil, ErrNotFound
}

func (m *memStore) ListRefs(ctx context.Context, repoID int64) ([]Ref, error) {
	m.d.mu.RLock()
	defer m.d.mu.RUnlock()
	out := []Ref{}
	if r, ok := m.d.repos[repoID]; ok {
		for n, oid := range r.refs {
			out = append(out, Ref{Name: n, OID: oid})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name }) // bytewise
	return out, nil
}

// setRefLocked writes a ref and records how to restore it. The ref must point
// at a stored object of the same repository (ErrForeignKey).
func (m *memStore) setRefLocked(r *memRepo, name, oid string) error {
	if _, ok := r.objects[oid]; !ok {
		return fmt.Errorf("%w: object %s", ErrForeignKey, oid)
	}
	prev, had := r.refs[name]
	r.refs[name] = oid
	m.record(func() {
		if cur, ok := m.d.repos[r.id]; ok && cur == r {
			if had {
				r.refs[name] = prev
			} else {
				delete(r.refs, name)
			}
		}
	})
	return nil
}

func (m *memStore) CreateRef(ctx context.Context, repoID int64, name, oid string) error {
	m.d.mu.Lock()
	defer m.d.mu.Unlock()
	r, ok := m.d.repos[repoID]
	if !ok {
		return fmt.Errorf("%w: repository %d", ErrForeignKey, repoID)
	}
	if _, taken := r.refs[name]; taken {
		return ErrConflict
	}
	return m.setRefLocked(r, name, oid)
}

func (m *memStore) DeleteRef(ctx context.Context, repoID int64, name string) error {
	m.d.mu.Lock()
	defer m.d.mu.Unlock()
	return m.deleteRefLocked(repoID, name, false)
}

func (m *memStore) DeleteNonDefaultRef(ctx context.Context, repoID int64, name string) error {
	m.d.mu.Lock()
	defer m.d.mu.Unlock()
	return m.deleteRefLocked(repoID, name, true)
}

func (m *memStore) deleteRefLocked(repoID int64, name string, keepDefault bool) error {
	r, ok := m.d.repos[repoID]
	if !ok {
		return ErrNotFound
	}
	oid, ok := r.refs[name]
	if !ok {
		return ErrNotFound
	}
	if keepDefault && r.defaultBranch == name {
		return errDefaultBranch
	}
	delete(r.refs, name)
	m.record(func() {
		if cur, ok := m.d.repos[r.id]; ok && cur == r {
			r.refs[name] = oid
		}
	})
	return nil
}

func (m *memStore) UpdateRef(ctx context.Context, repoID int64, name, oldOID, newOID string) error {
	if err := checkRefOIDs(oldOID, newOID); err != nil {
		return err
	}
	m.d.mu.Lock()
	defer m.d.mu.Unlock()
	return m.casLocked(repoID, name, oldOID, newOID)
}

// casLocked is the compare-and-swap of a branch: an empty oldOID means "must
// not exist" and creates it. ErrConflict if the swap lost.
func (m *memStore) casLocked(repoID int64, name, oldOID, newOID string) error {
	r, ok := m.d.repos[repoID]
	if !ok {
		if oldOID == "" {
			return fmt.Errorf("%w: repository %d", ErrForeignKey, repoID)
		}
		return ErrConflict
	}
	cur, exists := r.refs[name]
	if oldOID == "" {
		if exists {
			return ErrConflict
		}
	} else if !exists || cur != oldOID {
		return ErrConflict
	}
	return m.setRefLocked(r, name, newOID)
}

func (m *memStore) ResolveCommit(ctx context.Context, repoID int64, ref string) (string, error) {
	m.d.mu.RLock()
	defer m.d.mu.RUnlock()
	r, ok := m.d.repos[repoID]
	if !ok {
		return "", ErrNotFound
	}
	if oid, ok := r.refs[ref]; ok {
		return oid, nil
	}
	if !gitobj.ValidOID(ref) {
		return "", ErrNotFound
	}
	if _, ok := r.commit(ref); !ok {
		return "", ErrNotFound
	}
	return ref, nil
}

func (m *memStore) PutObjectsMoveRef(ctx context.Context, repoID int64, objs []treebuild.Object, name, oldOID, newOID string) error {
	if err := checkRefOIDs(oldOID, newOID); err != nil {
		return err
	}
	oids := make([]string, len(objs))
	for i, ob := range objs {
		oids[i] = gitobj.Hash(ob.Type, ob.Body)
	}
	m.d.mu.Lock()
	defer m.d.mu.Unlock()
	if err := m.putLocked(repoID, oids, objs); err != nil {
		return err
	}
	// Like the SQL statement, the objects stay stored when the swap loses;
	// the caller rolls back its InTx.
	return m.casLocked(repoID, name, oldOID, newOID)
}

// History walk and ancestry.

func (m *memStore) WalkFirstParents(ctx context.Context, repoID int64, tip string, limit int) ([]CommitRow, error) {
	if limit < 1 {
		return []CommitRow{}, nil
	}
	m.d.mu.RLock()
	defer m.d.mu.RUnlock()
	r := m.d.repos[repoID]
	out := []CommitRow{}
	for oid := tip; oid != "" && len(out) < limit && r != nil; {
		c, ok := r.commit(oid)
		if !ok {
			break
		}
		row := CommitRow{OID: oid, Data: cloneBytes(c.body)}
		if mt := parentHeader.FindSubmatch(head(c.body)); mt != nil {
			row.Parent = string(mt[1])
		}
		out = append(out, row)
		oid = row.Parent
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: %s", treebuild.ErrNotFound, tip)
	}
	if last := out[len(out)-1]; len(out) < limit && last.Parent != "" {
		return nil, fmt.Errorf("%w: %s", treebuild.ErrNotFound, last.Parent)
	}
	return out, nil
}

// reachable returns every commit reachable from start, following all parents.
func (r *memRepo) reachable(start string) map[string]bool {
	seen := map[string]bool{}
	if _, ok := r.commit(start); !ok {
		return seen
	}
	queue := []string{start}
	seen[start] = true
	for len(queue) > 0 {
		oid := queue[0]
		queue = queue[1:]
		c, _ := r.commit(oid)
		header := c.body
		if i := bytes.Index(header, []byte("\n\n")); i >= 0 {
			header = header[:i]
		}
		for _, line := range strings.Split(string(header), "\n") {
			mt := parentLine.FindStringSubmatch(line)
			if mt == nil || seen[mt[1]] {
				continue
			}
			if _, ok := r.commit(mt[1]); ok {
				seen[mt[1]] = true
				queue = append(queue, mt[1])
			}
		}
	}
	return seen
}

func (m *memStore) CommitReachability(ctx context.Context, repoID int64, a, b string) (Reachability, error) {
	m.d.mu.RLock()
	defer m.d.mu.RUnlock()
	r, ok := m.d.repos[repoID]
	if !ok {
		return Reachability{}, nil
	}
	return Reachability{AFromB: r.reachable(b)[a], BFromA: r.reachable(a)[b]}, nil
}
