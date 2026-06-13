package core

import (
	"database/sql"
	"sync"
)

type ExecutionCoordinator struct {
	dbMu sync.RWMutex
	ioMu sync.Map // Map of filepath -> *sync.Mutex
	DB   *sql.DB
}

func NewExecutionCoordinator(db *sql.DB) *ExecutionCoordinator {
	return &ExecutionCoordinator{
		DB: db,
	}
}

func (ec *ExecutionCoordinator) GetFileMutex(filepath string) *sync.Mutex {
	mu, _ := ec.ioMu.LoadOrStore(filepath, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

func (ec *ExecutionCoordinator) LockFileWrite(filepath string, task func()) {
	fMu := ec.GetFileMutex(filepath)
	fMu.Lock()
	defer fMu.Unlock()
	
	task()
}

func (ec *ExecutionCoordinator) LockDBWrite(task func()) {
	ec.dbMu.Lock()
	defer ec.dbMu.Unlock()
	task()
}

func (ec *ExecutionCoordinator) LockDBRead(task func()) {
	ec.dbMu.RLock()
	defer ec.dbMu.RUnlock()
	task()
}
