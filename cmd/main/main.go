package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/log"

	"zeaf.dev/distcluststore"
)

// Env constants
const (
	PodIP              = "POD_IP"
	Hostname           = "HOSTNAME"
	TotalClusters      = "TOTAL_CLUSTERS"
	Cluster            = "CLUSTER"
	LookUpHost         = "LOOKUP_HOST"
	MountPath          = "MOUNT_PATH"
	ClusterHostPattern = "CLUSTER_HOST_PATTERN" // cluster-%d.kvstore.dev
)

const (
	Port = "9090"
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
	cluster, err := strconv.Atoi(os.Getenv(Cluster))
	if err != nil {
		log.Fatal(err)
	}
	log.Infof("CLUSTER: %d", cluster)
	totalClustersStr := os.Getenv(TotalClusters)
	totalClusters, err := strconv.Atoi(totalClustersStr)
	if err != nil {
		log.Fatal(err)
	}
	log.Infof("CLUSTERS: %d", totalClusters)
	ip := os.Getenv(PodIP)
	lookuphost := os.Getenv(LookUpHost)
	log.Infof("LOOKUP_HOST: %s", lookuphost)
	mountPath := os.Getenv(MountPath)
	log.Infof("MOUNT_PATH: %s", mountPath)

	clusterHostPattern := os.Getenv(ClusterHostPattern)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store, err = distcluststore.NewStore(
		ctx,
		mountPath,
		distcluststore.ClusterConfig{
			ID:                 id,
			TotalClusters:      totalClusters,
			Cluster:            cluster,
			LookupHost:         lookuphost,
			ClusterHostPattern: clusterHostPattern,
			IP:                 net.ParseIP(ip),
			Port:               9987,
			Forwards:           2,
		},
	)
	if err != nil {
		log.Fatal("Err", "error", err)
	}

	sigch := make(chan os.Signal, 1)
	signal.Notify(sigch, syscall.SIGINT, syscall.SIGTERM)

	mux := &http.ServeMux{}
	mux.HandleFunc("/v1/kv/update", HandleUpdate)
	mux.HandleFunc("/v1/kv/get/{key}", HandleGet)
	log.Info("KV_SERVER_STARTING")
	server := &http.Server{
		Addr:    ":9090",
		Handler: mux,
	}
	go func() {
		select {
		case <-ctx.Done():
			log.Info("RECV_CTX_DONE")
		case <-sigch:
			log.Info("RECV_SIGTERM")
			ctx, c := context.WithTimeout(context.Background(), 5*time.Second)
			defer c()
			server.Shutdown(ctx)
			cancel()
		}
	}()
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
	log.Info("Shutting down server")
}

func HandleUpdate(w http.ResponseWriter, r *http.Request) {
	log.Info("UPDATE_RECV_START")
	bytes, _ := io.ReadAll(r.Body)
	log.Info("UPDATE_RECV", "body", string(bytes))
	req := UpdateRequest{}
	json.Unmarshal(bytes, &req)
	if red := store.GetRedirect(req.Key); red != "" {
		url := fmt.Sprintf("http://%s:%s%s", red, Port, r.URL.Path) // Ensure correct concatenation
		log.Info("Redirect", "url", url)
		http.Redirect(w, r, url, http.StatusTemporaryRedirect) // Use 301 or 302 as needed
		return
	}
	store.Set(req.Key, req.Value)
	w.WriteHeader(200)
}

func HandleGet(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	log.Info("GET_RECV", "key", key)
	if red := store.GetRedirect(key); red != "" {
		url := fmt.Sprintf("http://%s:%s%s", red, Port, r.URL.Path) // Ensure correct concatenation
		log.Info("Redirect", "url", url)
		http.Redirect(w, r, url, http.StatusTemporaryRedirect) // Use 301 or 302 as needed
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
