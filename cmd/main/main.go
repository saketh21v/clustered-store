package main

import (
	"fmt"
	"log"
	"math/rand"

	distcluststore "zeaf.dev/kvstore"
)

func main() {
	s, err := distcluststore.NewStore()
	if err != nil {
		log.Fatal("Err", "error", err)
	}
	val, _ := s.Get("key1")
	fmt.Println("key1", val)
	s.Set("key1", fmt.Sprintf("val-%d", rand.Intn(100)))
	s.Close()
}
