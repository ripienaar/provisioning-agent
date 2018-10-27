package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"sync"
	"time"

	"github.com/choria-io/go-backplane/backplane"
	"github.com/ghodss/yaml"
)

// Version is the version of the app
var Version = "0.0.0"

// Config is the configuration structure
type Config struct {
	Workers          int                              `json:"workers"`
	Interval         string                           `json:"interval"`
	Logfile          string                           `json:"logfile"`
	Loglevel         string                           `json:"loglevel"`
	Helper           string                           `json:"helper"`
	Token            string                           `json:"token"`
	Insecure         bool                             `json:"choria_insecure"`
	Site             string                           `json:"site"`
	MonitorPort      int                              `json:"monitor_port"`
	UpdateRepository string                           `json:"update_repository"`
	Management       *backplane.StandardConfiguration `yaml:"management"`

	Features struct {
		PKI bool `json:"pki"`
	} `json:"features"`

	IntervalDuration time.Duration `json:"-"`
	File             string        `json:"-"`

	paused bool
	sync.Mutex
}

// Load reads configuration from a YAML file
func Load(file string) (*Config, error) {
	config := &Config{
		File: file,
	}

	if _, err := os.Stat(file); os.IsNotExist(err) {
		return nil, fmt.Errorf("file %s not found", file)
	}

	c, err := ioutil.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("config file could not be read: %s", err)
	}

	j, err := yaml.YAMLToJSON(c)
	if err != nil {
		return nil, fmt.Errorf("file %s could not be parsed: %s", file, err)
	}

	err = json.Unmarshal(j, &config)
	if err != nil {
		return nil, fmt.Errorf("could not parse config file %s as YAML: %s", file, err)
	}

	config.IntervalDuration, err = time.ParseDuration(config.Interval)
	if err != nil {
		return nil, fmt.Errorf("invalid duration: %s", err)
	}

	if config.IntervalDuration < time.Minute {
		return nil, errors.New("interval is too small, minmum is 1 minute.  Valid example values are 10m or 10h")
	}

	pausedGauge.WithLabelValues(config.Site).Set(0)

	return config, nil
}
