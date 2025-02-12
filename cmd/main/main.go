package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/log"
	"zeaf.dev/distcluststore"
)

// Env constants
const (
	PodIP    = "POD_IP"
	Hostname = "HOSTNAME"
	Nodes    = "NODES"
)

type (
	GetRequest struct {
		Key string `json:"key,omitempty"`
	}
	GetResponse struct {
		Key   string `json:"key,omitempty"`
		Value string `json:"value,omitempty"`
	}
	UpdateRequest struct {
		Key   string `json:"key,omitempty"`
		Value string `json:"value,omitempty"`
	}
)

var store *distcluststore.Store

func main() {
	hostname := os.Getenv(Hostname)
	log.Infof("HOSTNAME %s", hostname)
	parts := strings.Split(hostname, "-")
	log.Info("PARTS", "parts", parts)
	id, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		log.Fatal(err)
	}
	nodes := os.Getenv(Nodes)
	log.Infof("TOTAL_NODES %s", nodes)
	totalNodes, err := strconv.Atoi(nodes)
	if err != nil {
		log.Fatal(err)
	}
	ip := os.Getenv(PodIP)
	ctx := context.Background()
	store, err = distcluststore.NewStore(
		ctx,
		".",
		distcluststore.ClusterConfig{
			ID:                 id,
			Nodes:              totalNodes,
			Hostname:           "dstore.",
			ClusterHostPattern: "",
			IP:                 net.ParseIP(ip),
			Port:               9987,
			Forwards:           2,
		},
	)
	if err != nil {
		log.Fatal("Err", "error", err)
	}
	http.HandleFunc("/v1/kv/update", HandleUpdate)
	http.HandleFunc("/v1/kv/get/{key}", HandleGet)
	log.Info("KV_SERVER_STARTING")
	if err := http.ListenAndServe(":9090", nil); err != nil {
		log.Fatal(err)
	}
}

func HandleUpdate(w http.ResponseWriter, r *http.Request) {
	log.Info("UPDATE_RECV_START")
	bytes, _ := io.ReadAll(r.Body)
	log.Info("UPDATE_RECV", "body", string(bytes))
	req := UpdateRequest{}
	json.Unmarshal(bytes, &req)
	if red := store.GetRedirect(req.Key); red != "" {
		http.Redirect(w, r, red, http.StatusFound) // 302 Found
		return
	}
	store.Set(req.Key, req.Value)
	w.WriteHeader(200)
}

func HandleGet(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	log.Info("GET_RECV", "key", key)
	if red := store.GetRedirect(key); red != "" {
		log.Info("Redirect", "url", red)
		http.Redirect(w, r, red, http.StatusFound) // 302 Found
		return
	}
	val, err := store.Get(key)
	if err != nil {
		w.WriteHeader(500)
		return
	}
	res := GetResponse{
		Key:   key,
		Value: val,
	}

	bytes, _ := json.Marshal(res)
	w.Header().Add("Content-Type", "application/json")
	w.Write(bytes)
}
