package distcluststore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/rs/xid"
	"golang.org/x/exp/rand"
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

type HybridVecClock struct {
	Vec []uint64 `json:"vec,omitempty"`
	// Timestamp isn't going to be precise, but works for now. Used for conflict resolution
	Timestamp time.Time `json:"timestamp,omitempty"`
	mu        *sync.RWMutex
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

type Node struct {
	ID int
	IP net.IP
}

type ClusterConfig struct {
	ID                 int
	TotalClusters      int
	NodesPerCluster    int
	Cluster            int
	LookupHost         string
	ClusterHostPattern string // Ex: "cluster-%d.gossip.default.svc.cluster.local:9090", %d will be replaced with ID
	IP                 net.IP
	Port               int
	Forwards           int // Number of nodes to forward the event to
}

type Event struct {
	ID      string         `json:"id"` // node.ID + xid
	Source  int            `json:"source"`
	From    int            `json:"from"`
	Clock   HybridVecClock `json:"clock,omitempty"`
	Payload []byte         `json:"payload,omitempty"`
}

func (e Event) serialize(_ format) ([]byte, error) {
	return json.Marshal(e)
}

type State struct {
	State map[string]struct{} `json:"state,omitempty"`
	Data  []byte              `json:"data,omitempty"`
}

type Cluster struct {
	cfg            ClusterConfig
	clock          HybridVecClock
	nodes          []Node
	msgcallback    func([]byte)
	state          map[string]struct{}
	smu            *sync.RWMutex
	nmu            *sync.RWMutex
	client         *http.Client
	info           Node
	getCurrentData func() []byte // Gets the store data payload to be sent to other nodes
}

func NewCluster(
	ctx context.Context,
	cfg ClusterConfig,
	onmsgcallback func([]byte),
	getCurrentData func() []byte,
) (*Cluster, []byte, error) {
	log.Info("Creating cluster", "config", cfg)
	c := &Cluster{
		cfg: cfg,
		clock: HybridVecClock{
			Vec:       make([]uint64, cfg.NodesPerCluster),
			Timestamp: time.Time{},
			mu:        &sync.RWMutex{},
		},
		msgcallback: onmsgcallback,
		nodes:       make([]Node, 0, 10),
		state:       make(map[string]struct{}),
		smu:         &sync.RWMutex{},
		nmu:         &sync.RWMutex{},
		client:      &http.Client{},
		info: Node{
			ID: cfg.ID,
			IP: cfg.IP,
		},
		getCurrentData: getCurrentData,
	}
	if err := c.discoverNodes(); err != nil {
		log.Error("ERR_DISCOVER_NODES", "error", err)
		return nil, nil, err
	}
	payload := c.updateInitialState()
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		for {
			select {
			case <-ctx.Done():
				log.Info("REFRESH_GO_ROUTINE", "msg", "exit")
				return
			case <-ticker.C:
				c.discoverNodes()
			}
		}
	}()
	go c.startServer(ctx)
	log.Info("CLUSTER_READY")
	return c, payload, nil
}

func (c *Cluster) update(eTime time.Time, payload []byte) error {
	c.clock.Vec[c.cfg.ID] += 1
	c.clock.Timestamp = eTime

	ev := Event{
		ID:      fmt.Sprintf("%d:%s", c.cfg.ID, xid.New().String()),
		Source:  c.cfg.ID,
		From:    c.cfg.ID,
		Clock:   c.clock.clone(),
		Payload: payload,
	}
	log.Info("EVENT_GENERATED", "event", ev)
	c.smu.Lock()
	c.state[ev.ID] = struct{}{}
	c.smu.Unlock()
	c.forward(ev)
	return nil
}

func (c *Cluster) startServer(ctx context.Context) {
	s := http.NewServeMux()
	s.HandleFunc(V1InfoRoute, c.HandleInfo)
	s.HandleFunc(V1PostMessage, c.HandleMessaage)
	s.HandleFunc(V1StateRoute, c.HandleState)
	sv := http.Server{
		Addr:    "0.0.0.0:" + strconv.Itoa(c.cfg.Port),
		Handler: s,
		BaseContext: func(l net.Listener) context.Context {
			return ctx
		},
	}
	sv.ListenAndServe()
}

func (c *Cluster) discoverNodes() error {
	c.nmu.Lock()
	defer c.nmu.Unlock()
	nodes := make([]Node, 0, 10)
	ips, err := net.LookupIP(c.cfg.LookupHost)
	if err != nil {
		log.Errorf("Error %s", err)
		// TODO: Return actual error
		return nil
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

func (c *Cluster) updateInitialState() []byte {
	log.Info("Starting initial state update")
	c.smu.Lock()
	defer c.smu.Unlock()
	if len(c.nodes) == 0 || c.cfg.ID == 0 { // Either no other nodes or this is the first node
		return nil
	}
	// Fetch state from previous node
	n := c.cfg.ID - 1
	s, err := c.fetchState(c.nodes[n])
	if err != nil {
		log.Error("FETCH_STATE", "error", err)
		s = &State{
			State: map[string]struct{}{},
			Data:  []byte{},
		}
	}

	c.state = s.State
	log.Info("Initial state updated")
	return s.Data
}

func (c *Cluster) fetchState(node Node) (
	*State,
	error,
) {
	url := fmt.Sprintf("http://%s:%d"+V1StateRoute, node.IP.String(), c.cfg.Port)
	res, err := c.client.Get(url)
	if err != nil {
		return nil, err
	}
	bytes, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	log.Info("RECV_STATE", "state", string(bytes))
	state := &State{}
	if err := json.Unmarshal(bytes, state); err != nil {
		return nil, err
	}
	if len(state.State) == 0 {
		state.State = make(map[string]struct{})
	}
	return state, nil

}

func (c *Cluster) onmessage(ev Event) {
	log.Info(
		"EVENT_RECEIED",
		"event",
		ev,
	)
	log.Info("EV_CLOCK_CMP", "mine", c.clock, "event", ev.Clock)
	if _, ok := c.state[ev.ID]; ok || ev.Source == c.cfg.ID {
		log.Info("EVENT_RECV_IGNORED", "event", ev, "reason", "duplicate")
		return
	}
	// Compare clocks
	switch c.clock.compare(ev.Clock) {
	case CmpGt: // Event is the latest
		c.clock.merge(ev.Clock)
		c.smu.Lock()
		c.state[ev.ID] = struct{}{}
		c.smu.Unlock()
	default: // Event is older - ignore
		log.Info("EVENT_RECV_IGNORED_OLDER", "event", ev, "reason", "older event")
		return
	}
	c.msgcallback(ev.Payload)
	c.forward(ev)
}

func (c *Cluster) forward(ev Event) {
	if len(c.nodes) == 0 {
		return
	}
	c.nmu.RLock()
	defer c.nmu.RUnlock()
	from := ev.From
	ev.From = c.cfg.ID
	bs, _ := ev.serialize(F_JSON)
	nodes := rand.Perm(len(c.nodes))[:min(len(c.nodes), c.cfg.Forwards)]
	for _, i := range nodes {
		node := c.nodes[i]
		if node.ID == from {
			continue
		}
		res, err := c.client.Post(
			fmt.Sprintf(
				"http://%s:%d"+V1PostMessage,
				node.IP.String(),
				c.cfg.Port,
			),
			"application/json",
			bytes.NewReader(bs),
		)
		if err != nil || (res != nil && res.StatusCode != 200) {
			log.Error(
				"ERROR_FORWARD",
				"node",
				node,
				"error",
				err,
				"status_code",
				Zeroed(res).StatusCode,
			)
		}
	}
}

func (c *Cluster) getCluster(key string) string {
	n := hashModulo(key, c.cfg.TotalClusters)
	log.Info("getCluster", "expected", n, "cluster", c.cfg.Cluster)
	if n == c.cfg.Cluster {
		return ""
	}
	redirect := fmt.Sprintf(c.cfg.ClusterHostPattern, n)
	log.Infof("REDIRECT TO: %s", redirect)
	return redirect
}

func hashModulo(key string, N int) int {
	hash := sha256.Sum256([]byte(key))
	num := binary.BigEndian.Uint64(hash[:8])
	return int(num % uint64(N))
}
