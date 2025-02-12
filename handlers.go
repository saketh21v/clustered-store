package distcluststore

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/charmbracelet/log"
)

func (c *Cluster) HandleInfo(w http.ResponseWriter, r *http.Request) {
	bytes, err := json.Marshal(c.info)
	if err != nil {
		w.WriteHeader(500)
		return
	}
	w.Header().Add("Content-Type", "application/json")
	w.Write(bytes)
}

func (c *Cluster) HandleState(w http.ResponseWriter, r *http.Request) {
	state := &State{
		State: c.state,
		Data:  c.getCurrentData(),
	}
	bs, err := json.Marshal(state)
	if err != nil {
		w.WriteHeader(500)
		return
	}
	w.Header().Add("Content-Type", "application/json")
	w.Write(bs)
}

func (c *Cluster) HandleMessaage(w http.ResponseWriter, r *http.Request) {
	bytes, err := io.ReadAll(r.Body)
	if err != nil {
		log.Error("MESSAGE_RECV_ERR", "error", err)
		w.WriteHeader(500)
		return
	}

	ev := Event{}
	if err := json.Unmarshal(bytes, &ev); err != nil {
		log.Error("MESSAGE_RECV_UNMARSHALL_ERR", "error", err)
		return
	}
	if _, ok := c.state[ev.ID]; ok {
		log.Info("DUPLICATE", "msg", ev)
		return
	}
	w.WriteHeader(200)
	c.onmessage(ev)
}
