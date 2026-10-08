// Package executionguard bounds remote entry points, not arbitrary descendants.
package executionguard

import (
	"errors"
	unit "github.com/komari-monitor/komari-agent/monitoring/unit"
	"sync"
	"time"
)

type Options struct {
	SessionMaintenanceSeconds int    `json:"session_maintenance_seconds,omitempty"`
	Revision                  uint64 `json:"revision"`
	Managed                   bool   `json:"managed"`
	Maintenance               bool   `json:"maintenance"`
	MaxParallel               int    `json:"max_parallel"`
	DefaultTimeoutSeconds     int    `json:"default_timeout_seconds"`
	MaxTimeoutSeconds         int    `json:"max_timeout_seconds"`
	OutputBytes               int    `json:"output_bytes"`
	MinFreePercent            int    `json:"min_free_percent"`
	MaintenanceSeconds        int    `json:"maintenance_seconds"`
	ForceCloseOnTimeout       bool   `json:"force_close_on_timeout"`
}

func DefaultOptions() Options {
	return Options{DefaultTimeoutSeconds: 1800, MaxTimeoutSeconds: 21600, OutputBytes: 2 << 20, MinFreePercent: 10, MaintenanceSeconds: 900}
}
func (o Options) Validate() error {
	if o.SessionMaintenanceSeconds != 0 && (o.SessionMaintenanceSeconds < 30 || o.SessionMaintenanceSeconds > 3600) {
		return errors.New("invalid maintenance session deadline")
	}
	if o.MaxParallel < 0 || o.MaxParallel > 64 || o.DefaultTimeoutSeconds < 1 || o.DefaultTimeoutSeconds > o.MaxTimeoutSeconds || o.MaxTimeoutSeconds > 7*86400 || o.OutputBytes < 32768 || o.OutputBytes > 8<<20 || o.MinFreePercent < 0 || o.MinFreePercent > 50 || o.MaintenanceSeconds < 30 || o.MaintenanceSeconds > 3600 {
		return errors.New("invalid remote execution policy")
	}
	return nil
}

type Resource struct {
	MemoryTotal uint64    `json:"memory_total"`
	MemoryUsed  uint64    `json:"memory_used"`
	UpdatedAt   time.Time `json:"updated_at"`
	Fresh       bool      `json:"fresh"`
	Scope       string    `json:"scope"`
}

func Memory() Resource {
	m := unit.Ram()
	return Resource{MemoryTotal: m.Total, MemoryUsed: m.Used, UpdatedAt: time.Now().UTC(), Fresh: true, Scope: m.Mode}
}
func Capacity(total uint64) int {
	switch {
	case total == 0:
		return 1
	case total < 1<<30:
		return 5
	case total < 2<<30:
		return 8
	case total < 3<<30:
		return 10
	default:
		return 12
	}
}

type Controller struct {
	mu       sync.Mutex
	options  Options
	slots    map[string]bool
	Resource func() Resource
}

func New() *Controller {
	return &Controller{options: DefaultOptions(), slots: map[string]bool{}, Resource: Memory}
}

var Global = New()

func (c *Controller) Configure(o Options) error {
	if e := o.Validate(); e != nil {
		return e
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if o.Revision < c.options.Revision {
		return nil
	}
	o.Managed = false
	o.Maintenance = false
	o.SessionMaintenanceSeconds = 0
	c.options = o
	return nil
}
func (c *Controller) Settings() Options { c.mu.Lock(); defer c.mu.Unlock(); return c.options }
func (c *Controller) Reserve(id string, maintenance bool) (func(), error) {
	r := c.Resource()
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.options
	hard := p.MaxParallel
	if hard == 0 {
		hard = Capacity(r.MemoryTotal)
	}
	normal, repair := 0, 0
	for _, m := range c.slots {
		if m {
			repair++
		} else {
			normal++
		}
	}
	if _, exists := c.slots[id]; exists {
		return nil, errors.New("execution already admitted")
	}
	if maintenance {
		if repair >= 1 {
			return nil, errors.New("maintenance slot busy")
		}
	} else {
		if normal >= hard {
			return nil, errors.New("remote execution slots busy")
		}
		if r.MemoryTotal > 0 && p.MinFreePercent > 0 && (r.MemoryUsed >= r.MemoryTotal || float64(r.MemoryTotal-r.MemoryUsed)/float64(r.MemoryTotal)*100 < float64(p.MinFreePercent)) {
			return nil, errors.New("remote memory pressure; new execution paused")
		}
	}
	c.slots[id] = maintenance
	var once sync.Once
	return func() { once.Do(func() { c.mu.Lock(); delete(c.slots, id); c.mu.Unlock() }) }, nil
}
