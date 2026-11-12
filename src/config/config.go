package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	PollingServer struct {
		URLs    []string      `yaml:"urls"`
		Token   string        `yaml:"token"`
		Timeout time.Duration `yaml:"timeout"`
	} `yaml:"polling_server"`
	SNMP struct {
		Concurrency          int           `yaml:"concurrency"`
		DiscoveryConcurrency int           `yaml:"discovery_concurrency"`
		Timeout              time.Duration `yaml:"timeout"`
		Retries              int           `yaml:"retries"`
	} `yaml:"snmp"`
	ConfigBackup struct {
		Concurrency int           `yaml:"concurrency"`
		Timeout     time.Duration `yaml:"timeout"`
	} `yaml:"config_backup"`
}

func New() *Config {
	c := &Config{}

	c.PollingServer.Timeout = 30 * time.Second
	c.SNMP.Concurrency = 128
	c.SNMP.DiscoveryConcurrency = 4
	c.SNMP.Timeout = 5 * time.Second
	c.SNMP.Retries = 1
	c.ConfigBackup.Concurrency = 4
	c.ConfigBackup.Timeout = 30 * time.Second

	return c
}

func (c *Config) ReadFile(file string) error {
	data, err := os.ReadFile(file)

	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	if err == nil {
		if err = yaml.Unmarshal(data, c); err != nil {
			return err
		}
	}

	c.readEnvVars()

	return c.validate()
}

func (c *Config) readEnvVars() {
	if v := os.Getenv("POLLING_SERVER_URLS"); len(v) > 0 {
		c.PollingServer.URLs = strings.Split(v, ",")
	}

	if v := os.Getenv("POLLING_SERVER_TOKEN"); len(v) > 0 {
		c.PollingServer.Token = v
	}

	urls := make([]string, 0, len(c.PollingServer.URLs))

	for _, value := range c.PollingServer.URLs {
		if value = strings.TrimRight(strings.TrimSpace(value), "/"); len(value) > 0 {
			urls = append(urls, value)
		}
	}

	c.PollingServer.URLs = urls
}

func (c *Config) validate() error {
	if len(c.PollingServer.URLs) < 1 {
		return errors.New("polling_server.urls (or POLLING_SERVER_URLS) needs at least one URL")
	}

	for _, value := range c.PollingServer.URLs {
		if parsed, err := url.Parse(value); err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || len(parsed.Host) < 1 {
			return fmt.Errorf("polling_server.urls: %q is not an http or https URL", value)
		}
	}

	if len(c.PollingServer.Token) < 1 {
		return errors.New("polling_server.token (or POLLING_SERVER_TOKEN) is required, generate one from the Pollers page of the dashboard")
	}

	if c.PollingServer.Timeout <= 0 {
		return errors.New("polling_server.timeout must be greater than 0")
	}

	if c.SNMP.Concurrency < 1 {
		return errors.New("snmp.concurrency must be at least 1")
	}

	if c.SNMP.DiscoveryConcurrency < 1 {
		return errors.New("snmp.discovery_concurrency must be at least 1")
	}

	if c.SNMP.Timeout <= 0 {
		return errors.New("snmp.timeout must be greater than 0")
	}

	if c.SNMP.Retries < 0 {
		return errors.New("snmp.retries must not be negative")
	}

	if c.ConfigBackup.Concurrency < 1 {
		return errors.New("config_backup.concurrency must be at least 1")
	}

	if c.ConfigBackup.Timeout <= 0 {
		return errors.New("config_backup.timeout must be greater than 0")
	}

	return nil
}
