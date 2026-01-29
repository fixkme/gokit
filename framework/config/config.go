package config

import (
	"encoding/json"
	"os"
)

var Config *AppConfig

type AppConfig struct {
	ServerId       int    `json:"server_id" mapstructure:"server_id"`
	AppVersion     string `json:"app_version" mapstructure:"app_version"`
	TimezoneOffset int    `json:"timezone_offset" mapstructure:"timezone_offset"` //时区偏移 秒
	LogConfig      `json:",inline" mapstructure:",inline"`
	RpcConfig      `json:",inline" mapstructure:",inline"`
	RedisConfig    `json:",inline" mapstructure:",inline"`
	MongoUri       string `json:"mongo_uri" mapstructure:"mongo_uri"`
	IsDebug        bool   `json:"is_debug" mapstructure:"is_debug"`
	PprofPort      int    `json:"pprof_port" mapstructure:"pprof_port"`
}

type RpcConfig struct {
	EtcdEndpoints   string `json:"etcd_endpoints" mapstructure:"etcd_endpoints"`       //etcd地址
	EtcdLeaseTTL    int64  `json:"etcd_lease" mapstructure:"etcd_lease_ttl"`           //注册服务到etcd的租约时间
	RpcGroup        string `json:"rpc_group" mapstructure:"rpc_group"`                 //rpc群组名称，群组之间隔离
	RpcAddr         string `json:"rpc_addr" mapstructure:"rpc_addr"`                   //本节点服务注册到etcd的地址，让其他节点作为客户端连接
	RpcListenAddr   string `json:"rpc_listen_addr" mapstructure:"rpc_listen_addr"`     //本节点服务监听的端口
	RpcPollerNum    int    `json:"rpc_poller_num" mapstructure:"rpc_poller_num"`       //rpc服务poller协程数量
	RpcReadTimeout  int    `json:"rpc_read_timeout" mapstructure:"rpc_read_timeout"`   //rpc服务读超时 毫秒
	RpcWriteTimeout int    `json:"rpc_write_timeout" mapstructure:"rpc_write_timeout"` //rpc服务写超时 毫秒
}

type LogConfig struct {
	LogPath   string `json:"log_path" mapstructure:"log_path"`
	LogName   string `json:"log_name" mapstructure:"log_name"`
	LogLevel  int    `json:"log_level" mapstructure:"log_level"`
	LogStdOut bool   `json:"log_std_out" mapstructure:"log_std_out"`
}

type RedisConfig struct {
	RedisMode       string `json:"redis_mode" mapstructure:"redis_mode"`
	RedisAddr       string `json:"redis_addr" mapstructure:"redis_addr"` // 多个地址用,隔开
	RedisMasterName string `json:"redis_master_name" mapstructure:"redis_master_name"`
	RedisPassword   string `json:"redis_password" mapstructure:"redis_password"`
	RedisDB         int    `json:"redis_db" mapstructure:"redis_db"`
}

func LoadConfig(configFile string, loadConfigFromEnv func(*AppConfig) error) error {
	Config = new(AppConfig)
	if len(configFile) == 0 {
		return loadConfigFromEnv(Config)
	}
	if err := loadConfigFromFile(configFile); err != nil {
		return err
	}
	if loadConfigFromEnv != nil {
		return loadConfigFromEnv(Config)
	}
	return nil
}

func loadConfigFromFile(configFile string) error {
	data, err := os.ReadFile(configFile)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &Config)
}

func (conf *AppConfig) JsonFormat() string {
	if conf == nil {
		return "{}"
	}
	data, err := json.MarshalIndent(conf, "", "  ")
	if err != nil {
		return ""
	}
	return string(data)
}
