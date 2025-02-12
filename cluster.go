package distcluststore

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/rs/xid"
)

// Route
const (
	V1StateRoute  = "/v1/gossip/state"
	V1InfoRoute   = "/v1/gossip/info"
	V1PostMessage = "/v1/gossip/message"
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
	Hostname           string
	ClusterHostPattern string // Ex: "cluster-%d.gossip.default.svc.cluster.local:9090", %d will be replaced with ID
	IP                 net.IP
	Port               int
}

type HybridVecClock struct {
	Vec []uint64 `json:"vec,omitempty"`
	// Timestamp isn't going to be precise, but works for now. Used for conflict resolution
	Timestamp time.Time `json:"timestamp,omitempty"`
	mu        *sync.RWMutex
}

type Node struct {
	ID int
	IP net.IP
}

type Cluster struct {
	cfg         ClusterConfig
	clock       HybridVecClock
	nodes       []Node
	msgcallback func([]byte)
	state       map[string]struct{}
	smu         *sync.Mutex
	nmu         *sync.Mutex
	client      *http.Client
}

type Event struct {
	ID      string         `json:"id"` // node.ID + xid
	Source  int            `json:"source"`
	Clock   HybridVecClock `json:"clock,omitempty"`
	Payload []byte         `json:"payload,omitempty"`
}

func NewCluster(
	cfg ClusterConfig,
	onmsgcallback func([]byte),
) (*Cluster, error) {
	c := &Cluster{
		cfg: cfg,
		clock: HybridVecClock{
			Vec:       make([]uint64, cfg.Nodes),
			Timestamp: time.Time{},
			mu:        &sync.RWMutex{},
		},
		msgcallback: onmsgcallback,
		nodes:       make([]Node, 0, 10),
		state:       make(map[string]struct{}),
		smu:         &sync.Mutex{},
		nmu:         &sync.Mutex{},
		client:      &http.Client{},
	}
	if err := c.discoverNodes(); err != nil {
		log.Error("ERR_DISCOVER_NODES", "error", err)
		return nil, err
	}
	return c, nil
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

func (c *HybridVecClock) merge(clk HybridVecClock) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range len(c.Vec) {
		c.Vec[i] = max(c.Vec[i], clk.Vec[i])
	}
}

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
	c.nmu.Lock()
	defer c.nmu.Unlock()
	nodes := make([]Node, 0, 10)
	ips, err := net.LookupIP(c.cfg.Hostname)
	if err != nil {
		log.Errorf("Error %s", err)
		return err
	}
	for _, ip := range ips {
		if ip.String() == c.cfg.IP.String() {
			continue
		}
		res, err := http.Get(fmt.Sprintf("http://%s:%d"+V1InfoRoute, ip, c.cfg.Port))
		if err != nil {
			log.Errorf("Error %s", err)
			continue
		}
		nodeInfo := &Node{}
		bytes, err := io.ReadAll(res.Body)
		if err != nil {
			log.Errorf("Error %s", err)
			continue
		}
		if err := json.Unmarshal(bytes, nodeInfo); err != nil {
			log.Errorf("Error %s", err)
			continue
		}
		nodes = append(nodes, *nodeInfo)
	}
	c.nodes = nodes
	return nil
}
func (c *Cluster) updateInitialState() error {
	log.Info("Starting initial state update")
	c.smu.Lock()
	defer c.smu.Unlock()
	if len(c.nodes) == 0 {
		return nil
	}
	// Fetch state from one random node
	node1 := rand.Intn(len(c.nodes))
	s1, err := c.fetchState(c.nodes[node1])
	if err != nil {
		log.Error("FETCH_STATE", "error", err)
	}

	c.state = s1
	log.Info("Initial state updated")
	return nil
}

func (c *Cluster) fetchState(node Node) (map[string]struct{}, error) {
	url := fmt.Sprintf("http://%s:%d"+V1StateRoute, node.IP.String(), c.cfg.Port)
	res, err := c.client.Get(url)
	if err != nil {
		return nil, err
	}
	bytes, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	state := make(map[string]struct{})
	if err := json.Unmarshal(bytes, &state); err != nil {
		return nil, err
	}
	return state, nil

}

func (c *Cluster) onmessage(e Event) {
	log.Info("EVENT_RECEIED", "event", e)
	if _, ok := c.state[e.ID]; ok || e.Source == c.cfg.ID {
		log.Info("EVENT_RECV_IGNORED", "event", e, "reason", "duplicate")
		return
	}
	// Compare clocks
	switch c.clock.compare(e.Clock) {
	case CmpGt: // Event is the latest
		c.clock.merge(e.Clock)
	default: // Event is older - ignore
		log.Info("EVENT_RECV_IGNORED", "event", e, "reason", "older event")
		return
	}
	c.msgcallback(e.Payload)
}

func (c *Cluster) getCluster(key string) string {
	n := hashModulo(key, c.cfg.Nodes)
	if n == c.cfg.ID {
		return ""
	}
	return fmt.Sprintf(c.cfg.ClusterHostPattern, n)
}

func hashModulo(key string, N int) int {
	hash := sha256.Sum256([]byte(key))
	num := binary.BigEndian.Uint64(hash[:8])
	return int(num % uint64(N))
}
