package distcluststore

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/rs/xid"
)

type ClockCmp uint

const (
	CmpConcurrent ClockCmp = iota
	CmpGt
	CmpLt
)

type ClusterConfig struct {
	ID                 int
	Nodes              int
	ClusterHostPattern string // Ex: "cluster-%d.gossip.default.svc.cluster.local:9090", %d will be replaced with ID
}

type HybridVecClock struct {
	Vec       []uint64  `json:"vec,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"` // Used for conflict resolution
	mu        *sync.RWMutex
}

type Node struct {
	ID int
	IP net.IP
}

type Cluster struct {
	cfg   ClusterConfig
	clock HybridVecClock
	nodes []Node
	seen  map[string]struct{}
	smu   *sync.Mutex
	nmu   *sync.Mutex
}

type Event struct {
	ID      string         `json:"id"` // node.ID + xid
	Source  int            `json:"source"`
	Clock   HybridVecClock `json:"clock,omitempty"`
	Payload []byte         `json:"payload,omitempty"`
}

func NewCluster(
	cfg ClusterConfig,
) (*Cluster, error) {
	c := &Cluster{
		cfg: cfg,
		clock: HybridVecClock{
			Vec:       make([]uint64, cfg.Nodes),
			Timestamp: time.Time{},
			mu:        &sync.RWMutex{},
		},
		nodes: make([]Node, 0, 10),
		seen:  make(map[string]struct{}),
		smu:   &sync.Mutex{},
		nmu:   &sync.Mutex{},
	}
	if err := c.discoverNodes(); err != nil {
		log.Error("ERR_DISCOVER_NODES", "error", err)
		return nil, err
	}
	return c, nil
}

func (c *Cluster) propagate(eTime time.Time, payload []byte) error {
	c.clock.Vec[c.cfg.ID] += 1
	c.clock.Timestamp = eTime

	ev := Event{
		ID:      fmt.Sprintf("%d:%s", c.cfg.ID, xid.New().String()),
		Clock:   c.clock.clone(),
		Payload: payload,
	}
	log.Info("EVENT_GENERATED", "event", ev)
	// TODO: @saketh - propagate
	return nil
}

func (c *Cluster) discoverNodes() error {
	// TODO: @saketh - Implement
	return nil
}

func (c *Cluster) onmessage(e Event) {
	log.Info("EVENT_RECEIED", "event", e)
	if _, ok := c.seen[e.ID]; ok || e.Source == c.cfg.ID {
		log.Info("EVENT_RECV_IGNORED", "event", e, "reason", "duplicate")
		return
	}
	// Compare clocks
	switch c.clock.compare(e.Clock) {
	case CmpGt: // Event is the latest
		// TODO: @saketh - Implement merge
		c.clock.merge(e.Clock)
	default: // Event is older - ignore
		log.Info("EVENT_RECV_IGNORED", "event", e, "reason", "older event")
	}

}

func (c HybridVecClock) compare(clk HybridVecClock) ClockCmp {
	a1 := clk.Vec
	a2 := c.Vec
	lt, gt := false, false
	for i := range len(a1) {
		if a1[i] > a2[i] {
			gt = true // Local clock is higher
		} else if a2[i] > a1[i] {
			lt = true // Even clock is higher
		}
	}
	switch {
	case lt && !gt:
		return CmpLt
	case gt && !lt:
		return CmpGt
	default:
		// Both lt and gt are true - Incomparable or Equal. Use timestamp
		// Incoming event is newer only if its timestamp is > local timestamp
		// NOTE: This is a decision - not universal
		if c.Timestamp.Compare(clk.Timestamp) == -1 {
			return CmpLt
		}
		return CmpGt
	}
}

func (c *HybridVecClock) merge(clk HybridVecClock) {}

func (c *HybridVecClock) clone() HybridVecClock {
	c.mu.RLock()
	defer c.mu.RUnlock()
	clk := HybridVecClock{
		Vec:       make([]uint64, len(c.Vec)),
		Timestamp: c.Timestamp,
	}
	copy(clk.Vec, c.Vec)
	return clk
}
