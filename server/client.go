package server

import (
	connection "github.com/isrc-cas/gt/conn"
	"sync"
	"sync/atomic"
)

type client struct {
	ID           string
	tunnels      map[*conn]struct{}
	tunnelsRWMtx sync.RWMutex
	tasks        map[uint32]*conn
	tasksRWMtx   sync.RWMutex
	taskIDSeed   uint32
	closeOnce    sync.Once
}

func newClient() interface{} {
	return &client{}
}

func (c *client) init(id string) {
	c.tunnelsRWMtx.Lock()
	c.ID = id
	c.tunnels = make(map[*conn]struct{})
	c.tunnelsRWMtx.Unlock()
	c.tasksRWMtx.Lock()
	c.tasks = make(map[uint32]*conn, 100)
	c.tasksRWMtx.Unlock()
}

func (c *client) process(task *conn) {
	id := atomic.AddUint32(&c.taskIDSeed, 1)
	if id >= connection.PreservedSignal {
		atomic.StoreUint32(&c.taskIDSeed, 1)
		id = 1
	}
	c.addTask(id, task)
	defer c.removeTask(id)

	tunnel := c.getTunnel()
	tunnel.process(id, task)
}

func (c *client) addTunnel(conn *conn) (ok bool) {
	c.tunnelsRWMtx.Lock()
	defer c.tunnelsRWMtx.Unlock()

	if c.tunnels == nil {
		return false
	}
	c.tunnels[conn] = struct{}{}
	return true
}

func (c *client) removeTunnel(conn *conn) {
	c.tunnelsRWMtx.Lock()
	delete(c.tunnels, conn)
	if len(c.tunnels) < 1 {
		c.tunnels = nil
		conn.server.removeClient(c.ID)
	}
	c.tunnelsRWMtx.Unlock()
}

func (c *client) getTunnel() (conn *conn) {
	c.tunnelsRWMtx.RLock()
	defer c.tunnelsRWMtx.RUnlock()
	var min uint32
	for t := range c.tunnels {
		count := t.GetTasksCount()
		if count == 0 {
			conn = t
			return
		}
		if min > count || conn == nil {
			min = count
			conn = t
		}
	}
	return
}

func (c *client) addTask(id uint32, conn *conn) {
	c.tasksRWMtx.Lock()
	c.tasks[id] = conn
	c.tasksRWMtx.Unlock()
}

func (c *client) removeTask(id uint32) {
	c.tasksRWMtx.Lock()
	delete(c.tasks, id)
	c.tasksRWMtx.Unlock()
}

func (c *client) getTask(id uint32) (conn *conn, ok bool) {
	c.tasksRWMtx.RLock()
	conn, ok = c.tasks[id]
	c.tasksRWMtx.RUnlock()
	return
}

func (c *client) close() {
	c.closeOnce.Do(func() {
		c.tasksRWMtx.Lock()
		for _, t := range c.tasks {
			t.Close()
		}
		c.tasksRWMtx.Unlock()
		c.tunnelsRWMtx.Lock()
		for t := range c.tunnels {
			t.SendCloseSignal()
			t.Close()
		}
		c.tunnelsRWMtx.Unlock()
	})
}

func (c *client) shutdown() {
	c.tasksRWMtx.Lock()
	for _, t := range c.tasks {
		t.Shutdown()
	}
	c.tasksRWMtx.Unlock()
	c.tunnelsRWMtx.Lock()
	for t := range c.tunnels {
		t.Shutdown()
	}
	c.tunnelsRWMtx.Unlock()
	return
}
