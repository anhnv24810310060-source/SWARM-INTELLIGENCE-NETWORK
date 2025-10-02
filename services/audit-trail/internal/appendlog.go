package internal

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// Entry represents an immutable audit log record.
type Entry struct {
	Index     uint64    `json:"index"`
	Timestamp time.Time `json:"ts"`
	Action    string    `json:"action"`
	Actor     string    `json:"actor"`
	Resource  string    `json:"resource"`
	Metadata  string    `json:"metadata"`
	PrevHash  string    `json:"prev_hash"`
	Hash      string    `json:"hash"`
}

// AppendLog is an in-memory append-only log with Merkle-like chaining (hash of record + prev hash).
// Future: segment to disk (WAL) + periodic snapshot.
type AppendLog struct {
	mu  sync.RWMutex
	log []Entry
}

func NewAppendLog() *AppendLog { return &AppendLog{log: make([]Entry, 0, 1024)} }

func (a *AppendLog) Append(action, actor, resource, metadata string) Entry {
	a.mu.Lock()
	defer a.mu.Unlock()
	idx := uint64(len(a.log))
	prev := ""
	if idx > 0 {
		prev = a.log[idx-1].Hash
	}
	ent := Entry{Index: idx, Timestamp: time.Now().UTC(), Action: action, Actor: actor, Resource: resource, Metadata: metadata, PrevHash: prev}
	ent.Hash = hashEntry(ent)
	a.log = append(a.log, ent)
	return ent
}

func (a *AppendLog) Get(index uint64) (Entry, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if index >= uint64(len(a.log)) {
		return Entry{}, false
	}
	return a.log[index], true
}

func (a *AppendLog) Latest() (Entry, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if len(a.log) == 0 {
		return Entry{}, false
	}
	return a.log[len(a.log)-1], true
}

func (a *AppendLog) Verify() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for i := range a.log {
		if hashEntry(a.log[i]) != a.log[i].Hash {
			return false
		}
		if i > 0 && a.log[i-1].Hash != a.log[i].PrevHash {
			return false
		}
	}
	return true
}

func hashEntry(e Entry) string {
	h := sha256.New()
	h.Write([]byte(e.PrevHash))
	h.Write([]byte(e.Timestamp.Format(time.RFC3339Nano)))
	h.Write([]byte(e.Action))
	h.Write([]byte(e.Actor))
	h.Write([]byte(e.Resource))
	h.Write([]byte(e.Metadata))
	return hex.EncodeToString(h.Sum(nil))
}
