package config

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v2"
)

// supported mount filesystem types
const (
	// FsTypeNFS - to mount NS filesystem over NFS
	FsTypeNFS string = "nfs"

	// FsTypeCIFS - to mount NS filesystem over SMB
	FsTypeCIFS                string = "cifs"
	DefaultInsecureSkipVerify        = true
)

// SuppertedFsTypeList - list of supported filesystem types to mount
var SuppertedFsTypeList = []string{FsTypeNFS, FsTypeCIFS}

// configuration address format
var regexpAddress = regexp.MustCompile("^https://[^:]+:[0-9]{1,5}$")

// Config - driver config from file
type Config struct {
	NodeMap     map[string]BsrData `yaml:"brickstor_map"`
	Debug       bool               `yaml:"debug,omitempty"`
	filePath    string
	lastModTime time.Time
	temporary   bool
	mutex       sync.Mutex // protects NodeMap
}

type BsrData struct {
	Address               string `yaml:"restIp"`
	Username              string `yaml:"username"`
	Password              string `yaml:"password"`
	DefaultDataset        string `yaml:"defaultDataset,omitempty"`
	DefaultDataIP         string `yaml:"defaultDataIp,omitempty"`
	DefaultMountFsType    string `yaml:"defaultMountFsType,omitempty"`
	DefaultMountOptions   string `yaml:"defaultMountOptions,omitempty"`
	Zone                  string `yaml:"zone"`
	MountPointPermissions string `yaml:"mountPointPermissions"`
	InsecureSkipVerify    *bool  `yaml:"insecureSkipVerify,omitempty"`
}

// GetFilePath - get filepath of found config file
func (c *Config) GetFilePath() string {
	return c.filePath
}

func (c *Config) LookupNode(name string) (BsrData, bool) {
	c.mutex.Lock()
	node, ok := c.NodeMap[name]
	c.mutex.Unlock()

	return node, ok
}

// NodeNames returns a sorted list of the node names in the NodeMap
func (c *Config) NodeNames() []string {

	var nodes []string

	c.mutex.Lock()
	for name, _ := range c.NodeMap {
		nodes = append(nodes, name)
	}
	c.mutex.Unlock()

	sort.Strings(nodes)

	return nodes
}

// Refresh - read and validate config, return `true` if config has been changed
func (c *Config) Refresh(secret string) (changed bool, err error) {
	if c.filePath == "" {
		return false, fmt.Errorf("Cannot read config file, filePath not specified")
	}

	fileInfo, err := os.Stat(c.filePath)
	if err != nil {
		return false, fmt.Errorf("Cannot access config file (%s): %s", c.filePath, err)
	}

	changed = c.lastModTime != fileInfo.ModTime() || len(secret) > 0 || c.temporary
	var content []byte
	if changed {
		c.mutex.Lock()
		defer c.mutex.Unlock()

		if len(secret) > 0 {
			content = []byte(secret)
			c.temporary = true
		} else {
			c.lastModTime = fileInfo.ModTime()
			c.temporary = false
			content, err = ioutil.ReadFile(c.filePath)
			if err != nil {
				return changed, fmt.Errorf("Cannot read config file (%s): %s", c.filePath, err)
			}
		}
		if err := yaml.Unmarshal(content, c); err != nil {
			return changed, fmt.Errorf("Cannot parse config file (%s): %s", c.filePath, err)
		}

		if err := c.validate(); err != nil {
			return changed, err
		}
	}

	return changed, nil
}

func (c *Config) validate() error {
	var errors []string

	for name, data := range c.NodeMap {
		if data.Address == "" {
			errors = append(errors, fmt.Sprintf("parameter 'restIp' is missing"))
		} else {
			addresses := strings.Split(data.Address, ",")
			for _, address := range addresses {
				if !regexpAddress.MatchString(address) {
					errors = append(errors,
						fmt.Sprintf("[node: %s] parameter 'restIp' has invalid address: '%s', should be 'https://host:port'",
							name, address),
					)
				}
			}
		}
		if data.Username == "" {
			errors = append(errors, fmt.Sprintf("parameter 'username' is missing"))
		}
		if data.Password == "" {
			errors = append(errors, fmt.Sprintf("parameter 'password' is missing"))
		}
		if data.DefaultMountFsType != "" && !containsString(SuppertedFsTypeList, data.DefaultMountFsType) {
			errors = append(
				errors,
				fmt.Sprintf("parameter 'defaultMountFsType' must be omitted or one of: [%s, %s]", FsTypeNFS, FsTypeCIFS),
			)
		}
		if data.InsecureSkipVerify == nil {
			insecureSkipVerify := DefaultInsecureSkipVerify
			data.InsecureSkipVerify = &insecureSkipVerify
			c.NodeMap[name] = data
		}

		if len(errors) != 0 {
			return fmt.Errorf("[noed: %s] Bad format, fix following issues: %s", name, strings.Join(errors, "; "))
		}

	}

	return nil
}

// containsString returns true if an array contains a string, otherwise false
func containsString(array []string, value string) bool {
	for _, v := range array {
		if v == value {
			return true
		}
	}
	return false
}

// findConfigFile - look up for config file in a directory
func findConfigFile(lookUpDir string) (configFilePath string, err error) {
	err = filepath.Walk(lookUpDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		ext := filepath.Ext(path)
		if ext == ".yaml" || ext == ".yml" {
			configFilePath = path
			return filepath.SkipDir
		}
		return nil
	})
	return configFilePath, err
}

// New - find config file and create config instance
func New(lookUpDir string) (*Config, error) {
	configFilePath, err := findConfigFile(lookUpDir)
	if err != nil {
		return nil, fmt.Errorf("Cannot read config directory '%s': %s", lookUpDir, err)
	} else if configFilePath == "" {
		return nil, fmt.Errorf("Cannot find .yaml config file in '%s' directory", lookUpDir)
	}

	// read config file
	config := &Config{filePath: configFilePath}
	if _, err := config.Refresh(""); err != nil {
		return nil, fmt.Errorf("Cannot refresh config from file '%s': %s", configFilePath, err)
	}

	return config, nil
}
