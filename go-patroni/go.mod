module github.com/patroni/patroni-go

go 1.22

require (
	github.com/go-chi/chi/v5 v5.1.0
	github.com/hashicorp/consul/api v1.28.2
	github.com/hashicorp/raft v1.6.1
	github.com/jackc/pgx/v5 v5.6.0
	github.com/rs/zerolog v1.33.0
	github.com/samuel/go-zookeeper v0.0.0-20201211165307-7117e9ea2414
	github.com/shirou/gopsutil/v3 v3.24.5
	github.com/spf13/cobra v1.8.1
	github.com/spf13/viper v1.19.0
	go.etcd.io/etcd/client/v3 v3.5.15
	gopkg.in/yaml.v3 v3.0.1
	k8s.io/api v0.30.3
	k8s.io/apimachinery v0.30.3
	k8s.io/client-go v0.30.3
)
