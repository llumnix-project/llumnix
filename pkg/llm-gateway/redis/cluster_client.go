package redis

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"context"

	"github.com/redis/go-redis/v9"
	"k8s.io/klog/v2"
)

type Config struct {
	RedisCluster RedisClusterConfig `json:"redis_cluster"`
}

type RedisClusterConfig struct {
	Hosts                []string       `json:"hosts"`
	Config               ClusterOptions `json:"config"`
	MaxBatchSize         int            `json:"max_batch_size"`
	MaxConcurrentBatches int            `json:"max_concurrent_routines"`
	QueryTimeout         time.Duration  `json:"query_timeout_second"`
	LogConfig            LogConfig      `json:"log_config"`
}

type ClusterOptions struct {
	ConnectTimeoutMs int    `json:"connect_timeout_ms"`
	SocketTimeoutMs  int    `json:"socket_timeout_ms"`
	PoolSize         int    `json:"pool_size"`
	Password         string `json:"password"`
}

type LogConfig struct {
	FilePath   string `json:"file_path"`
	MaxSizeMB  int    `json:"max_size_mb"`
	MaxBackups int    `json:"max_backups"`
}

// NewRedisClusterClient Modified from MetadataService src/go/redis_wrapper.go Initialize.
func NewRedisClusterClient(
	configPath string,
	redisClusterHosts string,
	redisClusterPassword string) (*redis.ClusterClient, *Config, error) {
	// Without using self defined logger here, use klog here to align with cms read client.

	var cfg *Config
	if configPath != "" {
		configData, err := os.ReadFile(configPath)
		if err != nil {
			return nil, nil, fmt.Errorf("read json file failed: %w", err)
		}

		// decode
		var tempConfig Config
		if err := json.Unmarshal(configData, &tempConfig); err != nil {
			return nil, nil, fmt.Errorf("decode json file failed: %w", err)
		}
		cfg = &tempConfig
	} else if redisClusterHosts != "" && redisClusterPassword != "" {
		cfg = &Config{
			RedisCluster: RedisClusterConfig{
				Hosts: strings.Split(redisClusterHosts, ","),
				Config: ClusterOptions{
					ConnectTimeoutMs: 5000,
					SocketTimeoutMs:  1000,
					PoolSize:         10,
					Password:         redisClusterPassword,
				},
				MaxBatchSize:         500,
				MaxConcurrentBatches: 10,
				QueryTimeout:         time.Second * 5,
				LogConfig: LogConfig{
					FilePath:   "/opt/meta_service/redis_go.LOG",
					MaxSizeMB:  5,
					MaxBackups: 10,
				},
			},
		}
	} else {
		return nil, nil, fmt.Errorf("neither config path nor config args provided")
	}

	clusterClient, err := connectRedisCluster(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("RedisCluster Initialize error: %w", err)
	}
	klog.Info("RedisCluster Initialize.")

	return clusterClient, cfg, nil
}

func connectRedisCluster(cfg *Config) (*redis.ClusterClient, error) {
	redisCfg := cfg.RedisCluster
	opts := &redis.ClusterOptions{
		Addrs:        redisCfg.Hosts,
		Password:     redisCfg.Config.Password,
		DialTimeout:  time.Duration(redisCfg.Config.ConnectTimeoutMs) * time.Millisecond,
		ReadTimeout:  time.Duration(redisCfg.Config.SocketTimeoutMs) * time.Millisecond,
		WriteTimeout: time.Duration(redisCfg.Config.SocketTimeoutMs) * time.Millisecond,
		PoolSize:     redisCfg.Config.PoolSize,
	}

	clusterClient := redis.NewClusterClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(),
		time.Duration(redisCfg.Config.ConnectTimeoutMs)*time.Millisecond)
	defer cancel()

	return clusterClient, clusterClient.Ping(ctx).Err()
}
