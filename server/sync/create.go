package sync

import (
	"sync/atomic"
	"unsafe"
)

// LoadOrCreate returns the existing value for the key if present.
// Otherwise, it creates and returns the given value.
// The loaded result is true if the value was loaded, false if created.
func (m *Map) LoadOrCreate(key interface{}, createFn func() interface{}) (actual interface{}, loaded bool) {
	read, _ := m.read.Load().(readOnly)
	if e, ok := read.m[key]; ok {
		actual, loaded, ok := e.tryLoadOrCreate(createFn)
		if ok {
			return actual, loaded
		}
	}

	m.mu.Lock()
	read, _ = m.read.Load().(readOnly)
	if e, ok := read.m[key]; ok {
		if e.unexpungeLocked() {
			m.dirty[key] = e
		}
		actual, loaded, _ = e.tryLoadOrCreate(createFn)
	} else if e, ok := m.dirty[key]; ok {
		actual, loaded, _ = e.tryLoadOrCreate(createFn)
		m.missLocked()
	} else {
		if !read.amended {
			m.dirtyLocked()
			m.read.Store(readOnly{m: read.m, amended: true})
		}
		value := createFn()
		m.dirty[key] = newEntry(value)
		actual, loaded = value, false
	}
	m.mu.Unlock()

	return actual, loaded
}

func (e *entry) tryLoadOrCreate(createFn func() interface{}) (actual interface{}, loaded, ok bool) {
	p := atomic.LoadPointer(&e.p)
	if p == expunged {
		return nil, false, false
	}
	if p != nil {
		return *(*interface{})(p), true, true
	}

	ic := createFn()
	for {
		if atomic.CompareAndSwapPointer(&e.p, nil, unsafe.Pointer(&ic)) {
			return ic, false, true
		}
		p = atomic.LoadPointer(&e.p)
		if p == expunged {
			return nil, false, false
		}
		if p != nil {
			return *(*interface{})(p), true, true
		}
	}
}
