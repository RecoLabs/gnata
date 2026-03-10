package gnata

import (
	"sync"
	"sync/atomic"

	"github.com/recolabs/gnata/internal/parser"
)

// GroupPlan caches per-schema evaluation metadata: which expressions are fast-path
// eligible for a given JSON schema (set of present fields).
type GroupPlan struct {
	// FastPaths[i] is the GJSON path for expressions[i] if fast-path eligible, or "" if not.
	FastPaths []string
	// ExprFastPath[i] is true if expressions[i] can use the zero-copy GJSON path.
	ExprFastPath []bool
	// CmpFast[i] is non-nil when expressions[i] is a path-vs-literal comparison
	// that can be short-circuited with a single gjson scan.
	CmpFast []*parser.ComparisonFastPath
	// FuncFast[i] is non-nil when expressions[i] is a supported built-in function
	// call on a pure path (e.g. $exists(a.b), $lowercase(name)).
	FuncFast []*parser.FuncFastPath
}

// cacheEntry is one slot in the BoundedCache.
type cacheEntry struct {
	key  string
	plan *GroupPlan
}

// BoundedCache is a fixed-capacity FIFO ring-buffer cache mapping string keys to *GroupPlan.
// On overflow the oldest entry is evicted.
// Reads are lock-free: Set publishes an atomic.Pointer snapshot; Get scans it without locking.
// Writes are serialised by a mutex. Linear scan is acceptable because the number of distinct
// schemas in practice is small (typically tens, not thousands).
type BoundedCache struct {
	mu       sync.Mutex
	capacity int
	entries  []cacheEntry
	head     int
	count    int
	snapshot atomic.Pointer[cacheSnapshot]

	hits      atomic.Int64
	misses    atomic.Int64
	evictions atomic.Int64
}

// cacheSnapshot holds both the slice and a map index for O(1) lookups.
type cacheSnapshot struct {
	entries []cacheEntry
	index   map[string]int // key → position in entries
}

// NewBoundedCache creates a new BoundedCache with the given capacity.
// Capacity must be at least 1; values <= 0 are clamped to 1.
func NewBoundedCache(capacity int) *BoundedCache {
	if capacity <= 0 {
		capacity = 1
	}
	c := &BoundedCache{
		capacity: capacity,
		entries:  make([]cacheEntry, capacity),
	}
	empty := &cacheSnapshot{entries: make([]cacheEntry, 0), index: make(map[string]int)}
	c.snapshot.Store(empty)
	return c
}

// Get looks up a key. Lock-free read path with O(1) map lookup.
func (c *BoundedCache) Get(key string) (*GroupPlan, bool) {
	snap := c.snapshot.Load()
	if i, ok := snap.index[key]; ok {
		c.hits.Add(1)
		return snap.entries[i].plan, true
	}
	c.misses.Add(1)
	return nil, false
}

// Set inserts or updates a key. Uses a mutex for writes.
// Returns true if an existing entry was evicted due to capacity overflow.
func (c *BoundedCache) Set(key string, plan *GroupPlan) (evicted bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	snap := c.snapshot.Load()
	if _, ok := snap.index[key]; ok {
		return false
	}

	if c.count >= c.capacity {
		c.evictions.Add(1)
		evicted = true
	}

	c.entries[c.head] = cacheEntry{key: key, plan: plan}
	c.head = (c.head + 1) % c.capacity
	if c.count < c.capacity {
		c.count++
	}

	newEntries := make([]cacheEntry, c.count)
	newIndex := make(map[string]int, c.count)
	for i := range c.count {
		idx := (c.head - c.count + i + c.capacity) % c.capacity
		newEntries[i] = c.entries[idx]
		newIndex[newEntries[i].key] = i
	}
	c.snapshot.Store(&cacheSnapshot{entries: newEntries, index: newIndex})
	return evicted
}

// Invalidate clears all cached GroupPlans. Subsequent EvalMany calls will
// rebuild plans from scratch. Safe to call concurrently.
func (c *BoundedCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.entries {
		c.entries[i] = cacheEntry{}
	}
	c.count = 0
	c.head = 0
	empty := &cacheSnapshot{entries: make([]cacheEntry, 0), index: make(map[string]int)}
	c.snapshot.Store(empty)
}

// Stats returns cache statistics.
func (c *BoundedCache) Stats() StreamStats {
	c.mu.Lock()
	count := int64(c.count)
	c.mu.Unlock()
	return StreamStats{
		Hits:      c.hits.Load(),
		Misses:    c.misses.Load(),
		Entries:   count,
		Evictions: c.evictions.Load(),
	}
}
