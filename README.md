# Distributed Clustered Store

Implementing a Distributed clustered key-value store for learning clocks and concensus

# Requirements/Limitations

- Partially Horizontally scalable - pods per cluster can be increased
    - Resizing clusters is not a goal at this point
- Highly read available at cluster level
	- System should be available as long as at least 1 node is available in the cluster
- Highly write available
	- System should be available as long as at least 2 nodes are available in the cluster
- Store is a Key-value store.
- Persistent

# Usage
`make container` 

`make run-kube`

## Examples

Run inside a container on
`curl -L distcluststore.default.svc.cluster.local:9090/v1/kv/get/k900`

`curl -L -X POST distcluststore.default.svc.cluster.local:9090/v1/kv/update --data-raw '{"key":"k1", "value":"v1"}'`
