// Copyright (c) 2023-2025, Nubificus LTD
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package unikontainers

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/sirupsen/logrus"
)

const UruncConfigPath = "/etc/urunc/config.toml"

type UruncLog struct {
	Level  string `toml:"level"`
	Syslog bool   `toml:"syslog"`
}

type UruncTimestamps struct {
	Enabled     bool   `toml:"enabled"`
	Destination string `toml:"destination"` // Used to specify a file for timestamps
}

type UruncConfig struct {
	Log UruncLog `toml:"log"`

	Timestamps UruncTimestamps `toml:"timestamps"`

	Hypervisors map[string]struct {
		DefaultMemoryMB int `toml:"default_memory_mb"`
		DefaultVCPUs    int `toml:"default_vcpus"`
	} `toml:"hypervisors"`
}

func DefaultUruncConfig() *UruncConfig {
	return &UruncConfig{
		Log: UruncLog{
			Level:  "info",
			Syslog: false,
		},
		Timestamps: UruncTimestamps{
			Enabled:     false,
			Destination: "/var/log/urunc/timestamps.log",
		},
		Hypervisors: map[string]struct {
			DefaultMemoryMB int `toml:"default_memory_mb"`
			DefaultVCPUs    int `toml:"default_vcpus"`
		}{
			"qemu":        {256, 1},
			"hvt":         {256, 1},
			"spt":         {256, 1},
			"firecracker": {256, 1},
		},
	}
}

func LoadOrCreateUruncConfig(path string) (*UruncConfig, error) {
	cfg := &UruncConfig{}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		logrus.Warnf("Config file %s not found, creating with defaults.", path)
		cfg = DefaultUruncConfig()
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		encoder := toml.NewEncoder(f)
		if err := encoder.Encode(cfg); err != nil {
			return nil, err
		}
		return cfg, nil
	}
	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
