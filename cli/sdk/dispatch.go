package sdk

import (
	"fmt"
	"sync"
)

type dispatchResult struct {
	value interface{}
	err   error
}

// dispatcher serializes all SDK work onto a single goroutine.
//
// gomobile can invoke exported Go methods from multiple threads; keeping all
// SDK state changes and transport interactions serialized prevents subtle races
// (and reduces the chance of runtime fatal aborts).
type dispatcher struct {
	once sync.Once
	q    chan func()
}

func newDispatcher(queueSize int) *dispatcher {
	if queueSize <= 0 {
		queueSize = 256
	}
	d := &dispatcher{
		q: make(chan func(), queueSize),
	}
	d.once.Do(func() {
		go func() {
			for fn := range d.q {
				if fn != nil {
					fn()
				}
			}
		}()
	})
	return d
}

func (d *dispatcher) do(fn func()) error {
	if d == nil {
		return fmt.Errorf("dispatcher not initialized")
	}
	if fn == nil {
		return nil
	}
	d.q <- fn
	return nil
}

func (d *dispatcher) call(fn func() (interface{}, error)) (interface{}, error) {
	if d == nil {
		return nil, fmt.Errorf("dispatcher not initialized")
	}
	if fn == nil {
		return nil, nil
	}
	done := make(chan dispatchResult, 1)
	d.q <- func() {
		value, err := fn()
		done <- dispatchResult{value: value, err: err}
	}
	res := <-done
	return res.value, res.err
}

