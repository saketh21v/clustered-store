package distcluststore

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/charmbracelet/log"
)

const (
	// WALFilePath = "/Users/zeaf/dev/dist-clust-store/wal.jsonl"
	WALFilePath = "/.store/wal.jsonl"
)

type Action uint

const (
	Unknown Action = iota
	StartWrite
	FinishedWrite
)

type WALEntry struct {
	Action Action    `json:"action,omitempty"`
	Key    string    `json:"key,omitempty"`
	Val    string    `json:"val,omitempty"` // Let's start with string, we can move to bytes or nested maps later
	Time   time.Time `json:"time,omitempty"`
	Source int       `json:"source,omitempty"`
}

type Store struct {
	mu      *sync.RWMutex
	m       map[string]string
	wal     io.WriteCloser
	wmu     *sync.Mutex
	cluster *Cluster
	id      int
}

func NewStore(
	mnt string, // Perisistent Volume mount path
	clusterCfg ClusterConfig,
) (*Store, error) {
	walpath := mnt + WALFilePath
	if err := os.MkdirAll(filepath.Dir(walpath), 0755); err != nil {
		return nil, err
	}
	// Read the WAL if present
	wal, err := os.OpenFile(mnt+WALFilePath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}
	s := &Store{
		mu:  &sync.RWMutex{},
		m:   make(map[string]string),
		wal: wal,
		wmu: &sync.Mutex{},
	}
	cluster, err := NewCluster(
		clusterCfg,
		func(b []byte) {
			e := WALEntry{}
			json.Unmarshal(b, &e)
			s.set(e.Key, e.Val, e.Source)
		},
	)
	if err != nil {
		return nil, err
	}
	s.cluster = cluster
	if err := s.loadFromWAL(wal); err != nil {
		return nil, err
	}
	wal.Seek(0, io.SeekEnd) // Make sure the file is append only
	return s, nil
}

// loadFromWAL reads the existing WriteAheadLog if present and loads the changes into the store
func (s *Store) loadFromWAL(f *os.File) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := f.Seek(0, io.SeekStart)
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		bs := scanner.Bytes()
		log.Infof("Line: %s", string(bs))
		entry := WALEntry{}
		if err := json.Unmarshal(bs, &entry); err != nil {
			log.Info("JSON_UNMARSHALL_ERROR", "error", err)
			return err
		}
		if entry.Action != StartWrite {
			continue
		}
		s.m[entry.Key] = entry.Val
	}
	log.Info("FILE", "file", f)
	return nil
}

func (s *Store) Set(key string, val string) error {
	now := time.Now()
	err := s.set(key, val, s.id)
	if err != nil {
		return err
	}
	finishBytes, err := json.Marshal(WALEntry{
		Action: FinishedWrite,
		Key:    key,
		Val:    val,
		Time:   now,
		Source: s.id,
	})
	if err != nil {
		return err
	}
	if err := s.cluster.propagate(now, finishBytes); err != nil {
		return err
	}
	return nil
}

func (s *Store) set(
	key string,
	val string,
	source int,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	startEnt := WALEntry{
		Action: StartWrite,
		Key:    key,
		Val:    val,
		Time:   time.Now(),
		Source: source,
	}
	startBytes, err := json.Marshal(startEnt)
	if err != nil {
		return err
	}
	startBytes = append(startBytes, []byte("\n")...)

	s.wmu.Lock()
	defer s.wmu.Unlock()
	if _, err := s.wal.Write(startBytes); err != nil {
		return err
	}
	s.m[key] = val
	finishEnt := WALEntry{
		Action: FinishedWrite,
		Key:    key,
		Val:    val,
		Time:   time.Now(),
		Source: source,
	}
	finishBytes, err := json.Marshal(finishEnt)
	if err != nil {
		return err
	}
	finishBytes = append(finishBytes, []byte("\n")...)
	if _, err := s.wal.Write(finishBytes); err != nil {
		// Don't return error to allow propagation to other nodes
		log.Error("FINISH_WAL_ERR", "error", err)
	}
	return nil
}

func (s *Store) Get(key string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.m[key], nil
}

func (s *Store) Close() {
	s.wal.Close()
}

func (s *Store) GetRedirect(key string) string {
	return s.cluster.getCluster(key)
}
