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
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultUruncConfig(t *testing.T) {
	cfg := DefaultUruncConfig()

	// Test log configuration
	assert.Equal(t, "info", cfg.Log.Level)
	assert.False(t, cfg.Log.Syslog)

	// Test timestamps configuration
	assert.False(t, cfg.Timestamps.Enabled)
	assert.Equal(t, "/var/log/urunc/timestamps.log", cfg.Timestamps.Destination)

	// Test hypervisors configuration
	assert.Len(t, cfg.Hypervisors, 4)

	// Test individual hypervisor configs
	hypervisors := []string{"qemu", "hvt", "spt", "firecracker"}
	for _, hypervisor := range hypervisors {
		assert.Contains(t, cfg.Hypervisors, hypervisor)
		assert.Equal(t, 256, cfg.Hypervisors[hypervisor].DefaultMemoryMB)
		assert.Equal(t, 1, cfg.Hypervisors[hypervisor].DefaultVCPUs)
	}
}

func TestLoadOrCreateUruncConfig_CreateNewConfig(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.toml")

	// Test creating a new config file when it doesn't exist
	cfg, err := LoadOrCreateUruncConfig(configPath)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	// Verify the file was created
	_, err = os.Stat(configPath)
	assert.NoError(t, err)

	// Verify the config has default values
	assert.Equal(t, "info", cfg.Log.Level)
	assert.False(t, cfg.Log.Syslog)
	assert.False(t, cfg.Timestamps.Enabled)
	assert.Equal(t, "/var/log/urunc/timestamps.log", cfg.Timestamps.Destination)
	assert.Len(t, cfg.Hypervisors, 4)
}

func TestLoadOrCreateUruncConfig_LoadExistingConfig(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.toml")

	// Create a custom config
	customConfig := &UruncConfig{
		Log: UruncLog{
			Level:  "debug",
			Syslog: true,
		},
		Timestamps: UruncTimestamps{
			Enabled:     true,
			Destination: "/custom/path/timestamps.log",
		},
		Hypervisors: map[string]struct {
			DefaultMemoryMB int `toml:"default_memory_mb"`
			DefaultVCPUs    int `toml:"default_vcpus"`
		}{
			"qemu": {512, 2},
			"hvt":  {1024, 4},
		},
	}

	// Write the custom config to file
	f, err := os.Create(configPath)
	require.NoError(t, err)
	defer f.Close()

	encoder := toml.NewEncoder(f)
	err = encoder.Encode(customConfig)
	require.NoError(t, err)

	// Test loading the existing config
	cfg, err := LoadOrCreateUruncConfig(configPath)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	// Verify the loaded config matches our custom config
	assert.Equal(t, "debug", cfg.Log.Level)
	assert.True(t, cfg.Log.Syslog)
	assert.True(t, cfg.Timestamps.Enabled)
	assert.Equal(t, "/custom/path/timestamps.log", cfg.Timestamps.Destination)
	assert.Len(t, cfg.Hypervisors, 2)
	assert.Equal(t, 512, cfg.Hypervisors["qemu"].DefaultMemoryMB)
	assert.Equal(t, 2, cfg.Hypervisors["qemu"].DefaultVCPUs)
	assert.Equal(t, 1024, cfg.Hypervisors["hvt"].DefaultMemoryMB)
	assert.Equal(t, 4, cfg.Hypervisors["hvt"].DefaultVCPUs)
}

func TestLoadOrCreateUruncConfig_InvalidTOMLFile(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.toml")

	// Create an invalid TOML file
	err := os.WriteFile(configPath, []byte("invalid toml content [[["), 0644)
	require.NoError(t, err)

	// Test loading the invalid config
	cfg, err := LoadOrCreateUruncConfig(configPath)
	assert.Error(t, err)
	assert.Nil(t, cfg)
}

func TestLoadOrCreateUruncConfig_PermissionError(t *testing.T) {
	// Try to create config in a non-existent directory with restricted permissions
	configPath := "/root/non-existent/config.toml"

	// This should fail due to permission issues (assuming we're not running as root)
	cfg, err := LoadOrCreateUruncConfig(configPath)
	assert.Error(t, err)
	assert.Nil(t, cfg)
}

func TestLoadOrCreateUruncConfig_DirectoryCreation(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "nested", "dir", "config.toml")

	// Test creating a new config file in nested directories
	cfg, err := LoadOrCreateUruncConfig(configPath)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	// Verify the directories were created
	_, err = os.Stat(filepath.Dir(configPath))
	assert.NoError(t, err)

	// Verify the file was created
	_, err = os.Stat(configPath)
	assert.NoError(t, err)
}

func TestUruncConfigStruct(t *testing.T) {
	// Test that we can create and manipulate UruncConfig struct
	cfg := &UruncConfig{
		Log: UruncLog{
			Level:  "error",
			Syslog: true,
		},
		Timestamps: UruncTimestamps{
			Enabled:     true,
			Destination: "/test/path",
		},
		Hypervisors: map[string]struct {
			DefaultMemoryMB int `toml:"default_memory_mb"`
			DefaultVCPUs    int `toml:"default_vcpus"`
		}{
			"test": {128, 1},
		},
	}

	assert.Equal(t, "error", cfg.Log.Level)
	assert.True(t, cfg.Log.Syslog)
	assert.True(t, cfg.Timestamps.Enabled)
	assert.Equal(t, "/test/path", cfg.Timestamps.Destination)
	assert.Contains(t, cfg.Hypervisors, "test")
	assert.Equal(t, 128, cfg.Hypervisors["test"].DefaultMemoryMB)
	assert.Equal(t, 1, cfg.Hypervisors["test"].DefaultVCPUs)
}

func TestUruncLogStruct(t *testing.T) {
	log := UruncLog{
		Level:  "warn",
		Syslog: false,
	}

	assert.Equal(t, "warn", log.Level)
	assert.False(t, log.Syslog)
}

func TestUruncTimestampsStruct(t *testing.T) {
	timestamps := UruncTimestamps{
		Enabled:     true,
		Destination: "/var/log/test.log",
	}

	assert.True(t, timestamps.Enabled)
	assert.Equal(t, "/var/log/test.log", timestamps.Destination)
}

func TestUruncConfigPath(t *testing.T) {
	// Test that the constant is defined correctly
	assert.Equal(t, "/etc/urunc/config.toml", UruncConfigPath)
}

func TestLoadOrCreateUruncConfig_ValidTOMLWithMismatchedContent(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.toml")

	// Create a valid TOML file but with content that doesn't fully match UruncConfig struct
	validTOMLWithMismatch := `
# This is a valid TOML file but has some mismatched content
[log]
level = "debug"
# Missing syslog field - should default to false

[timestamps]
enabled = true
# destination is missing - should default to empty string

# Missing hypervisors section entirely

# Extra fields that don't exist in UruncConfig
[extra_section]
unknown_field = "some_value"
extra_number = 42

[another_unknown]
data = ["array", "of", "strings"]
`

	err := os.WriteFile(configPath, []byte(validTOMLWithMismatch), 0644)
	require.NoError(t, err)

	// Test loading the config with mismatched content
	cfg, err := LoadOrCreateUruncConfig(configPath)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	// Verify that known fields are loaded correctly
	assert.Equal(t, "debug", cfg.Log.Level)
	assert.False(t, cfg.Log.Syslog) // Should default to false since it's missing

	// Verify that partial timestamps config is loaded
	assert.True(t, cfg.Timestamps.Enabled)
	assert.Equal(t, "", cfg.Timestamps.Destination) // Should be empty since it's missing

	// Verify that missing hypervisors section results in empty/nil map
	assert.Empty(t, cfg.Hypervisors)

	// The extra fields should be ignored and not cause any errors
	// This test verifies that TOML decoder gracefully handles extra fields
}

func TestLoadOrCreateUruncConfig_PartialValidTOML(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.toml")

	// Create a TOML file with only some sections defined
	partialTOML := `
[log]
level = "warn"
syslog = true

# Only log section, missing timestamps and hypervisors
`

	err := os.WriteFile(configPath, []byte(partialTOML), 0644)
	require.NoError(t, err)

	// Test loading the partial config
	cfg, err := LoadOrCreateUruncConfig(configPath)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	// Verify that defined fields are loaded correctly
	assert.Equal(t, "warn", cfg.Log.Level)
	assert.True(t, cfg.Log.Syslog)

	// Verify that missing sections use zero values
	assert.False(t, cfg.Timestamps.Enabled)
	assert.Equal(t, "", cfg.Timestamps.Destination)
	assert.Empty(t, cfg.Hypervisors)
}

func TestLoadOrCreateUruncConfig_TOMLWithWrongTypes(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.toml")

	// Create a TOML file with wrong data types
	wrongTypesTOML := `
[log]
level = 123  # Should be string, not number
syslog = "true"  # Should be boolean, not string

[timestamps]
enabled = "false"  # Should be boolean, not string
destination = true  # Should be string, not boolean
`

	err := os.WriteFile(configPath, []byte(wrongTypesTOML), 0644)
	require.NoError(t, err)

	// Test loading the config with wrong types - this should fail
	cfg, err := LoadOrCreateUruncConfig(configPath)
	assert.Error(t, err)
	assert.Nil(t, cfg)
}

func TestTOMLSerialization(t *testing.T) {
	// Test that the config can be properly serialized and deserialized
	cfg := DefaultUruncConfig()

	// Serialize to TOML
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "test_config.toml")

	f, err := os.Create(configPath)
	require.NoError(t, err)
	defer f.Close()

	encoder := toml.NewEncoder(f)
	err = encoder.Encode(cfg)
	require.NoError(t, err)
	f.Close()

	// Deserialize from TOML
	var loadedCfg UruncConfig
	_, err = toml.DecodeFile(configPath, &loadedCfg)
	require.NoError(t, err)

	// Compare the configurations
	assert.Equal(t, cfg.Log.Level, loadedCfg.Log.Level)
	assert.Equal(t, cfg.Log.Syslog, loadedCfg.Log.Syslog)
	assert.Equal(t, cfg.Timestamps.Enabled, loadedCfg.Timestamps.Enabled)
	assert.Equal(t, cfg.Timestamps.Destination, loadedCfg.Timestamps.Destination)
	assert.Len(t, loadedCfg.Hypervisors, len(cfg.Hypervisors))

	for name, hvConfig := range cfg.Hypervisors {
		assert.Contains(t, loadedCfg.Hypervisors, name)
		assert.Equal(t, hvConfig.DefaultMemoryMB, loadedCfg.Hypervisors[name].DefaultMemoryMB)
		assert.Equal(t, hvConfig.DefaultVCPUs, loadedCfg.Hypervisors[name].DefaultVCPUs)
	}
}
