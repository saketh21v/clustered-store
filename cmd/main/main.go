package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"net"

	"zeaf.dev/distcluststore"
)

func main() {
	ctx := context.Background()
	s, err := distcluststore.NewStore(
		ctx,
		".",
		distcluststore.ClusterConfig{
			ID:                 0,
			Nodes:              1,
			Hostname:           "",
			ClusterHostPattern: "",
			IP:                 net.ParseIP("0.0.0.0"),
			Port:               9090,
			Forwards:           2,
		},
	)
	if err != nil {
		log.Fatal("Err", "error", err)
	}
	val, _ := s.Get("key1")
	fmt.Println("key1", val)
	s.Set("key1", fmt.Sprintf("val-%d", rand.Intn(100)))
	s.Close()
}
